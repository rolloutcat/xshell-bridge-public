package bridge

import (
	"strings"
	"time"
)

type ExplainRequest struct {
	SessionID  string `json:"session_id"`
	Generation string `json:"generation"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
}
type ApprovalExplanation struct {
	Decision   string          `json:"decision"`
	ReasonCode string          `json:"reason_code"`
	Message    string          `json:"message"`
	MatchedID  string          `json:"matched_id,omitempty"`
	Checks     []ApprovalCheck `json:"checks"`
}
type ApprovalCheck struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Matched       bool   `json:"matched"`
	ReasonCode    string `json:"reason_code"`
	Message       string `json:"message"`
	ArgumentIndex *int   `json:"argument_index,omitempty"`
}
type BatchDecision struct {
	Allow           bool        `json:"allow"`
	Items           []BatchItem `json:"items"`
	RememberMinutes int         `json:"remember_minutes,omitempty"`
}
type BatchItem struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Generation  string `json:"generation"`
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Command     string `json:"command"`
	Cwd         string `json:"cwd"`
	TimeoutMS   int    `json:"timeout_ms"`
}

func validCommand(command, cwd string, timeout int) bool {
	return strings.TrimSpace(command) != "" && len(command) <= 16384 && !strings.ContainsAny(command, "\x00\r") && len(cwd) <= 4096 && !strings.ContainsAny(cwd, "\x00\r\n") && timeout >= 1000 && timeout <= 3600000
}

// targetReason is deliberately read-only; check invalidates expired attachments.
func (s *Store) targetReason(id, generation string, now time.Time) string {
	x := s.state.Sessions[id]
	if x == nil {
		return "session_missing"
	}
	if x.Generation != generation {
		return "generation"
	}
	if x.Archived || strings.TrimSpace(x.Fingerprint) == "" || x.ID != id || generation == "" {
		return "identity"
	}
	if x.State != "active" {
		return "inactive"
	}
	if now.Sub(x.LastSeen) > 15*time.Second {
		return "stale"
	}
	return ""
}
func approvalMessage(code string) string {
	switch code {
	case "identity":
		return "目标已移除或身份不完整"
	case "builtin_disabled":
		return "默认只读规则包已由操作员停用"
	case "builtin_timeout":
		return "默认规则仅支持 1000–60000 毫秒超时"
	case "builtin_cwd":
		return "默认规则需要规范绝对目录（仅 ASCII 字母、数字及 _./@+-）"
	case "builtin_length":
		return "默认规则命令最多 4096 字节"
	case "builtin_segments":
		return "默认规则最多 10 个简单命令段"
	case "builtin_syntax":
		return "默认规则仅支持有限字面 Shell 词法与逐段匹配的管道；不支持展开、重定向、替换、空段或复杂语法"
	case "builtin_environment":
		return "默认规则不支持环境变量赋值前缀；需要人工审批"
	case "builtin_program":
		return "程序不在默认只读规则目录中；需要人工审批"
	case "builtin_arguments":
		return "该程序的完整参数未列入默认只读规则；需要人工审批"
	case "stopping":
		return "服务正在停止，无法接受新提交"
	case "matched":
		return "范围完全匹配"
	case "expired":
		return "授权已过期"
	case "generation":
		return "终端连接代次不匹配"
	case "cwd":
		return "工作目录不匹配"
	case "timeout":
		return "超时时间不在授权范围内"
	case "token_syntax":
		return "命令包含规则不支持的语法，需要精确审批"
	case "program":
		return "程序不匹配"
	case "argument_count":
		return "参数数量不匹配"
	case "argument_pattern":
		return "参数未完全匹配规则"
	case "command":
		return "命令不完全相同"
	case "session_missing":
		return "目标终端不存在"
	case "inactive":
		return "目标终端未激活或已失效"
	case "stale":
		return "目标终端心跳已过期"
	}
	return "没有匹配的授权，需要人工审批"
}

// Explain describes eligibility of a NEW submission only. It neither approves
// pending jobs nor promises that a future dispatch will remain authorized.
func (s *Store) Explain(v ExplainRequest) (ApprovalExplanation, error) {
	if v.TimeoutMS == 0 {
		v.TimeoutMS = 60000
	}
	if !validCommand(v.Command, v.Cwd, v.TimeoutMS) {
		return ApprovalExplanation{}, fail("INVALID_ARGUMENT", "invalid command, cwd or timeout (1000..3600000 ms)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := ApprovalExplanation{Decision: "manual", ReasonCode: "approval_required", Message: "新提交需要人工审批；不会批准已有任务，也不保证执行", Checks: []ApprovalCheck{}}
	if s.stopping {
		out.Decision = "blocked"
		out.ReasonCode = "stopping"
		out.Message = approvalMessage("stopping")
		return out, nil
	}
	if code := s.targetReason(v.SessionID, v.Generation, now); code != "" {
		out.Decision = "blocked"
		out.ReasonCode = code
		out.Message = approvalMessage(code)
		return out, nil
	}
	if d := checkCommandPolicy(v.Command, v.Cwd); d != nil {
		out.Decision, out.ReasonCode, out.Message = "manual", "exact_approval_required", d.Message
		if d.Decision == "deny" {
			out.Decision, out.ReasonCode, out.MatchedID = "blocked", "command_denied", d.ID
		}
		out.Checks = append(out.Checks, ApprovalCheck{Kind: d.Decision, ID: d.ID, Matched: true, ReasonCode: out.ReasonCode, Message: d.Message})
		return out, nil
	}
	r := Submit{SessionID: v.SessionID, Generation: v.Generation, Command: v.Command, Cwd: v.Cwd, TimeoutMS: v.TimeoutMS}
	id, code, candidate := s.builtinMatch(r)
	message := approvalMessage(code)
	if candidate != "" {
		message += "。目录示例：" + candidate + "；示例不会代替原命令自动执行"
	}
	out.Checks = append(out.Checks, ApprovalCheck{Kind: "builtin", ID: BuiltinPackID, Name: "默认基础只读检查 v5", Matched: id != "", ReasonCode: code, Message: message})
	if id != "" {
		out.Decision = "builtin"
		out.MatchedID = id
	}
	if id == "" && needsExactApproval(v.Command) {
		out.ReasonCode, out.Message = "exact_approval_required", "脚本或无法静态确定的命令须逐次人工审批；不复用临时授权或参数规则"
		return out, nil
	}
	for _, g := range s.state.Grants {
		if g.SessionID != r.SessionID {
			continue
		}
		code := "matched"
		switch {
		case !now.Before(g.Expires):
			code = "expired"
		case g.Generation != r.Generation:
			code = "generation"
		case g.Cwd != r.Cwd:
			code = "cwd"
		case g.TimeoutMS != r.TimeoutMS:
			code = "timeout"
		case !g.matches(r):
			code = "command"
		}
		matched := code == "matched"
		out.Checks = append(out.Checks, ApprovalCheck{Kind: "grant", ID: g.ID, Matched: matched, ReasonCode: code, Message: approvalMessage(code)})
		if matched && out.MatchedID == "" {
			out.Decision = "grant"
			out.MatchedID = g.ID
		}
	}
	for _, rule := range s.state.Rules {
		if rule.SessionID != r.SessionID {
			continue
		}
		code, index := rule.matchReason(r)
		if !now.Before(rule.Expires) {
			code = "expired"
			index = nil
		}
		matched := code == "matched"
		out.Checks = append(out.Checks, ApprovalCheck{Kind: "rule", ID: rule.ID, Name: rule.Name, Matched: matched, ReasonCode: code, Message: approvalMessage(code), ArgumentIndex: index})
		if matched && out.MatchedID == "" {
			out.Decision = "rule"
			out.MatchedID = rule.ID
		}
	}
	if out.MatchedID != "" {
		out.ReasonCode = "matched"
		out.Message = "新提交符合当前授权策略；不会批准已有任务，也不保证执行"
	}
	return out, nil
}
func (s *Store) DecideBatch(v BatchDecision) error {
	return s.change(func() error {
		if v.RememberMinutes != 0 && (v.RememberMinutes != 30 || !v.Allow || len(v.Items) != 1) {
			return fail("INVALID_ARGUMENT", "remember requires exactly one allowed item and 30 minutes")
		}
		if len(v.Items) < 1 || len(v.Items) > 20 {
			return fail("INVALID_ARGUMENT", "batch requires 1..20 items")
		}
		seen := map[string]bool{}
		now := time.Now()
		for _, item := range v.Items {
			if seen[item.ID] {
				return fail("INVALID_ARGUMENT", "duplicate batch job ID")
			}
			seen[item.ID] = true
			j := s.state.Jobs[item.ID]
			if j == nil || j.Status != "pending" {
				return fail("CONFLICT", "batch job missing or not pending")
			}
			if s.targetReason(item.SessionID, item.Generation, now) != "" {
				return fail("CONFLICT", "batch target is no longer active and fresh")
			}
			x := s.state.Sessions[item.SessionID]
			if j.SessionID != item.SessionID || j.Generation != item.Generation || x.Fingerprint != item.Fingerprint || x.Name != item.Name || x.Environment != item.Environment || j.Command != item.Command || j.Cwd != item.Cwd || j.TimeoutMS != item.TimeoutMS {
				return fail("CONFLICT", "batch target or command changed")
			}
			if v.Allow {
				policy := checkCommandPolicy(j.Command, j.Cwd)
				if d := policy; d != nil && d.Decision == "deny" {
					return d.err()
				}
				if v.RememberMinutes != 0 && (policy != nil || needsExactApproval(j.Command)) {
					return fail("EXACT_APPROVAL_REQUIRED", "scripts and uninspectable commands cannot be remembered")
				}
			}
		}
		// No error-returning operations after preflight. change persists once and
		// restores the entire state, including revocations and audit, on failure.
		for _, item := range v.Items {
			s.decideLocked(s.state.Jobs[item.ID], v.Allow, v.RememberMinutes)
		}
		return nil
	})
}
