package bridge

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const BuiltinPackID = "builtin-readonly-v5"
const builtinMaxCommandBytes = 4096

// Pointer persistence distinguishes an absent setting from an operator opt-out.
type BuiltinPolicy struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Enabled  bool   `json:"enabled"`
	Revision uint64 `json:"revision"`
}
type PolicyChange struct {
	Expected BuiltinPolicy `json:"expected"`
	Enabled  bool          `json:"enabled"`
}
type ApprovalRecord struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	ID     string    `json:"id,omitempty"`
}

func (s *Store) builtinPolicy() BuiltinPolicy {
	return BuiltinPolicy{ID: BuiltinPackID, Version: 5, Enabled: s.state.BuiltinPolicyEnabled == nil || *s.state.BuiltinPolicyEnabled, Revision: s.state.BuiltinPolicyRevision}
}
func (s *Store) BuiltinPolicy() BuiltinPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.builtinPolicy()
}
func (s *Store) SetBuiltinPolicy(v PolicyChange) error {
	return s.change(func() error {
		if v.Expected != s.builtinPolicy() {
			return fail("CONFLICT", "displayed default policy changed; refresh before changing it")
		}
		if v.Enabled == v.Expected.Enabled {
			return nil
		}
		if s.state.BuiltinPolicyRevision == ^uint64(0) {
			return fail("CONFLICT", "policy revision exhausted")
		}
		value := v.Enabled
		s.state.BuiltinPolicyEnabled = &value
		s.state.BuiltinPolicyRevision++
		if !value {
			for _, j := range s.state.Jobs {
				if j.Status == "queued" && j.ApprovalSource == "builtin" {
					j.Status = "pending"
					j.RequiresExactApproval = needsExactApproval(j.Command)
					s.audit("approval_required", j.ID, "default policy disabled before dispatch; original provenance retained")
				}
			}
		}
		s.audit("builtin_policy", BuiltinPackID, map[bool]string{true: "enabled", false: "disabled"}[value]+"; existing pending and dispatched jobs unchanged")
		return nil
	})
}

var builtinCwd = regexp.MustCompile(`\A/[A-Za-z0-9_./@+-]*\z`)
var builtinUnit = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9_.@-]{0,119}\.service\z`)
var builtinAssignment = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_]*=`)
var builtinProperties = map[string]bool{"ActiveState": true, "SubState": true, "LoadState": true, "UnitFileState": true, "MainPID": true, "Result": true, "ExecMainStatus": true, "Description": true}

// Complete argv forms are the authority. The first form also supplies the
// program-specific, non-executable example used in explanations.
var builtinForms = []struct {
	program string
	forms   []string
}{
	{"hostname", []string{"hostname", "hostname -f"}},
	{"whoami", []string{"whoami"}},
	{"id", []string{"id", "id -u", "id -g", "id -G", "id -un", "id -gn"}},
	{"pwd", []string{"pwd"}},
	{"uptime", []string{"uptime"}},
	{"uname", []string{"uname -sr", "uname -a", "uname -s", "uname -r", "uname -n", "uname -m"}},
	{"date", []string{"date -u", "date", "date --utc", "date -Iseconds", "date +%F", "date +%T"}},
	{"free", []string{"free -h", "free -m", "free -g"}},
	{"df", []string{"df -h", "df -hT", "df -T", "df -i", "df -ih", "df -hi"}},
	{"nproc", []string{"nproc"}},
	{"lscpu", []string{"lscpu"}},
	{"vmstat", []string{"vmstat 1 1"}},
	{"ps", []string{"ps -eo pid,comm,pcpu,pmem --sort=-pcpu"}},
	{"ip", []string{"ip route show", "ip address show", "ip addr show", "ip -brief address show", "ip link show", "ip route list", "ip -6 route show"}},
	{"ss", []string{"ss -ltn", "ss -lntu", "ss -ltnp", "ss -lntp", "ss -lntup", "ss -lnutp", "ss --numeric --listening --tcp", "ss --numeric --listening --udp"}},
	{"systemctl", []string{"systemctl --no-pager status nginx.service", "systemctl status nginx.service --no-pager", "systemctl --no-pager is-active nginx.service", "systemctl --no-pager is-enabled nginx.service", "systemctl show nginx.service --no-pager -p ActiveState,SubState", "systemctl --no-pager list-units --type=service --no-legend", "systemctl list-units --failed --type=service --no-legend --no-pager", "systemctl --no-pager list-unit-files --type=service --no-legend"}},
}

// The catalogue is derived from the same forms used for matching and hints.
// It is separate from BuiltinPolicy, whose comparable snapshot is used for CAS.
type PolicyCategory struct {
	Category string   `json:"category"`
	Examples []string `json:"examples"`
}
type CommandPolicy struct {
	HardDeny          HardDenyPolicy   `json:"hard_deny"`
	Policy            BuiltinPolicy    `json:"policy"`
	Constraints       []string         `json:"constraints"`
	Categories        []PolicyCategory `json:"categories"`
	Notice            string           `json:"notice"`
	ServiceProperties []string         `json:"service_properties"`
	ReadLimits        ReadLimits       `json:"read_limits"`
}

type HardDenyPolicy struct {
	ID         string   `json:"id"`
	Enabled    bool     `json:"enabled"`
	Categories []string `json:"categories"`
	Notice     string   `json:"notice"`
}

type ReadLimits struct {
	CommandBytes   int `json:"command_bytes"`
	Words          int `json:"words"`
	Segments       int `json:"segments"`
	PipelineStages int `json:"pipeline_stages"`
	FilesPerStage  int `json:"files_per_stage"`
	NumericMaximum int `json:"numeric_maximum"`
	ByteMaximum    int `json:"byte_maximum"`
}

func (s *Store) CommandPolicy() CommandPolicy {
	properties := make([]string, 0, len(builtinProperties))
	for name := range builtinProperties {
		properties = append(properties, name)
	}
	sort.Strings(properties)
	categories := []PolicyCategory{}
	for _, rule := range builtinForms {
		category := "identity_and_time"
		switch rule.program {
		case "free", "df", "nproc", "lscpu", "vmstat", "ps":
			category = "resources"
		case "ip", "ss":
			category = "network_status"
		case "systemctl":
			category = "services"
		}
		index := -1
		for i := range categories {
			if categories[i].Category == category {
				index = i
				break
			}
		}
		if index < 0 {
			categories = append(categories, PolicyCategory{Category: category})
			index = len(categories) - 1
		}
		categories[index].Examples = append(categories[index].Examples, rule.forms...)
	}
	reads := PolicyCategory{Category: "file_and_journal_read"}
	for _, rule := range builtinReadExamples {
		reads.Examples = append(reads.Examples, rule.example)
	}
	reads.Examples = append(reads.Examples, "cat /var/log/app.log | grep -n -E 'ERROR|WARN' | head -n 100", "tail -c 16777216 /var/log/app.log | grep -F ERROR", "timeout -k 2s 25s grep -F -m 1 ERROR /var/log/app.log")
	categories = append(categories, reads)
	return CommandPolicy{HardDeny: HardDenyPolicy{ID: HardDenyPolicyID, Enabled: true, Categories: []string{"destructive_files_and_disks", "host_disruption", "sql", "redis", "mongo", "container_and_cluster", "infrastructure", "etcd", "kafka", "object_storage", "explicit_script_operations"}, Notice: "Definite destructive operations are denied before all approvals. Unknown/dynamic commands and scripts require fresh exact human approval, not automatic denial. Python log analysis remains available. Static inspection is not a sandbox; aliases, external files and runtime-generated behavior are not fully resolved. This policy cannot be disabled by the read-only toggle."}, Policy: s.BuiltinPolicy(), Categories: categories, ServiceProperties: properties, ReadLimits: ReadLimits{builtinMaxCommandBytes, builtinMaxWords, builtinMaxSegments, builtinMaxStages, builtinMaxFiles, builtinMaxCount, builtinMaxReadBytes}, Constraints: []string{
		"active fresh session with complete identity and exact generation", "canonical absolute cwd; timeout 1000..60000 ms; command at most 4096 bytes", "complete literal argv; at most 128 words, 10 segments and 8 pipeline stages", "ls, cat, grep, head and tail accept up to 32 literal paths per stage; -- permits names beginning with -; ls excludes recursion and unknown options", "head/tail byte counts: 0..67108864 (64 MiB); line counts and grep/journal counts: 0..1000000; signed GNU head/tail counts retain their original semantics, not a maximum total read size", "timeout: one wrapper, positive integer seconds (optional s), duration 1..60; optional -k/--kill-after 1..10 seconds; sum must fit task timeout; child argv and every pipeline stage must independently match read-only rules", "grep recursion may read many files; output and timeout limits still apply", "journalctl requires --no-pager and finite --lines; follow and write operations are excluded", "no shell expansion, globbing, redirection, environment assignments, interpreters or write operations; remote PATH, permissions and symlinks retain their normal semantics", "systemctl requires --no-pager, one literal service unit or bounded service list; --failed only for list-units", "show requires one -p or --property argument, comma-separated unique properties: " + strings.Join(properties, ", "),
	}, Notice: "Read-only discovery; does not execute or authorize. Examples do not guarantee future target or policy state. Existing tasks are unchanged."}
}

func builtinSystemctl(tokens []string) string {
	var verb, unit, properties string
	noPager, noLegend, serviceType, failed, propertySeen := false, false, false, false, false
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case t == "--no-pager":
			if noPager {
				return ""
			}
			noPager = true
		case t == "--no-legend":
			if noLegend {
				return ""
			}
			noLegend = true
		case t == "--type=service":
			if serviceType {
				return ""
			}
			serviceType = true
		case t == "--failed":
			if failed {
				return ""
			}
			failed = true
		case t == "-p" || t == "--property":
			if propertySeen || i+1 >= len(tokens) {
				return ""
			}
			i++
			propertySeen = true
			properties = tokens[i]
		case strings.HasPrefix(t, "--property="):
			if propertySeen {
				return ""
			}
			propertySeen = true
			properties = strings.TrimPrefix(t, "--property=")
		case t == "status" || t == "is-active" || t == "is-enabled" || t == "show" || t == "list-units" || t == "list-unit-files":
			if verb != "" {
				return ""
			}
			verb = t
		case builtinUnit.MatchString(t):
			if unit != "" || verb == "" {
				return ""
			}
			unit = t
		default:
			return ""
		}
	}
	if !noPager {
		return ""
	}
	switch verb {
	case "status", "is-active", "is-enabled":
		if unit == "" || propertySeen || noLegend || serviceType || failed {
			return ""
		}
	case "show":
		if unit == "" || !propertySeen || noLegend || serviceType || failed {
			return ""
		}
		seen := map[string]bool{}
		for _, p := range strings.Split(properties, ",") {
			if !builtinProperties[p] || seen[p] {
				return ""
			}
			seen[p] = true
		}
	case "list-units", "list-unit-files":
		if unit != "" || propertySeen || !noLegend || !serviceType || (failed && verb != "list-units") {
			return ""
		}
	default:
		return ""
	}
	return "systemctl-" + verb
}

func builtinForm(tokens []string) (id, candidate string) {
	for _, rule := range builtinForms {
		if tokens[0] != rule.program {
			continue
		}
		if rule.program == "systemctl" {
			if id := builtinSystemctl(tokens); id != "" {
				return id, ""
			}
			return "", rule.forms[0]
		}
		for _, form := range rule.forms {
			if sameBuiltinWords(tokens, strings.Fields(form)) {
				return rule.program, ""
			}
		}
		return "", rule.forms[0]
	}
	return "", ""
}

func sameBuiltinWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Match each complete parsed stage; no command is rewritten before dispatch.
func builtinCommand(v Submit) (string, string, string) {
	if v.TimeoutMS < 1000 || v.TimeoutMS > 60000 {
		return "", "builtin_timeout", ""
	}
	if !builtinCwd.MatchString(v.Cwd) || path.Clean(v.Cwd) != v.Cwd {
		return "", "builtin_cwd", ""
	}
	if len(v.Command) > builtinMaxCommandBytes {
		return "", "builtin_length", ""
	}
	segments, reason := builtinParse(v.Command)
	if reason != "" {
		return "", reason, ""
	}
	var ids []string
	for _, pipeline := range segments {
		var stages []string
		for i, tokens := range pipeline {
			wrapped := tokens[0] == "timeout"
			if wrapped {
				var ok bool
				tokens, ok = builtinTimeout(tokens, v.TimeoutMS)
				if !ok {
					return "", "builtin_arguments", "timeout -k 2s 25s grep -F -m 1 ERROR /var/log/app.log"
				}
			}
			if builtinAssignment.MatchString(tokens[0]) {
				return "", "builtin_environment", ""
			}
			program := tokens[0]
			if i > 0 && program != "grep" && program != "head" && program != "tail" {
				return "", "builtin_program", ""
			}
			readProgram, example := false, ""
			for _, rule := range builtinReadExamples {
				if program == rule.program {
					readProgram, example = true, rule.example
					break
				}
			}
			var id, candidate string
			if readProgram {
				if builtinRead(tokens, i > 0) {
					id = program
				} else {
					candidate = example
				}
			} else {
				id, candidate = builtinForm(tokens)
			}
			if id == "" {
				if candidate != "" {
					return "", "builtin_arguments", candidate
				}
				return "", "builtin_program", ""
			}
			if wrapped {
				id = "timeout-" + id
			}
			stages = append(stages, id)
		}
		ids = append(ids, strings.Join(stages, "-"))
	}
	return BuiltinPackID + "/" + strings.Join(ids, "+"), "matched", ""
}
func (s *Store) builtinMatch(v Submit) (string, string, string) {
	if !s.builtinPolicy().Enabled {
		return "", "builtin_disabled", ""
	}
	if reason := s.targetReason(v.SessionID, v.Generation, time.Now()); reason != "" {
		return "", reason, ""
	}
	return builtinCommand(v)
}
func (s *Store) recordApproval(j *Job, source, id string) {
	j.ApprovalSource = source
	j.ApprovalHistory = append(j.ApprovalHistory, ApprovalRecord{Time: time.Now().UTC(), Source: source, ID: id})
}
