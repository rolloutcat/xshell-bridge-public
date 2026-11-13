package bridge

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Selected jobs must predate the authority: creating a grant or rule must not
// retroactively approve them. All jobs and permissions use the real Store API.
func batchDenialFixture(t *testing.T) (*Store, BatchDecision, []string, []string) {
	t.Helper()
	s, x, spec := ruleFixture(t)
	submit := func(request, command, cwd string) Job {
		t.Helper()
		r := req(x)
		r.RequestID, r.Command, r.Cwd = request, command, cwd
		j, err := s.Submit(r)
		must(t, err)
		return j
	}
	a := submit("selected-a", "printf alpha", "")
	b := submit("selected-b", "printf beta", "")
	batch := BatchDecision{Allow: false}
	for _, j := range []Job{a, b} {
		batch.Items = append(batch.Items, BatchItem{ID: j.ID, SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, Name: x.Name, Environment: x.Environment, Command: j.Command, Cwd: j.Cwd, TimeoutMS: j.TimeoutMS})
	}
	seed := submit("grant-seed", a.Command, "")
	must(t, s.DecideRemember(seed.ID, true, 30))
	spec.ArgumentPatterns = []string{"beta"}
	_, err := s.CreateRule(spec)
	must(t, err)
	qa := submit("queued-a", a.Command, "")
	qb := submit("queued-b", b.Command, "")
	if qa.Status != "queued" || qa.GrantID == "" || qa.RuleID != "" || qb.Status != "queued" || qb.RuleID == "" || qb.GrantID != "" {
		t.Fatal("fixture did not exercise both authority types", qa, qb)
	}
	// Same commands on another cwd are unrelated scopes, not merely unrelated
	// command strings. Their queued jobs and permissions must survive denial.
	ua := submit("unrelated-grant-seed", a.Command, "/other")
	must(t, s.DecideRemember(ua.ID, true, 30))
	spec.Cwd = "/other"
	_, err = s.CreateRule(spec)
	must(t, err)
	uqa := submit("unrelated-a", a.Command, "/other")
	uqb := submit("unrelated-b", b.Command, "/other")
	if uqa.Status != "queued" || uqa.GrantID == "" || uqb.Status != "queued" || uqb.RuleID == "" {
		t.Fatal("unrelated scopes not queued", uqa, uqb)
	}
	for _, item := range batch.Items {
		j, err := s.GetJob(item.ID)
		must(t, err)
		if j.Status != "pending" || j.GrantID != "" || j.RuleID != "" {
			t.Fatal("authority retroactively approved selection", j)
		}
	}
	return s, batch, []string{qa.ID, qb.ID}, []string{uqa.ID, uqb.ID}
}

func TestBatchDenialRevokesQueuedAuthorityAtomically(t *testing.T) {
	for _, storageFailure := range []bool{false, true} {
		name := "commit"
		if storageFailure {
			name = "persist-failure"
		}
		t.Run(name, func(t *testing.T) {
			s, batch, related, unrelated := batchDenialFixture(t)
			before := stateJSON(t, s)
			disk, err := os.ReadFile(s.path)
			must(t, err)
			originalPath := s.path
			unrelatedBefore := map[string]Job{}
			for _, id := range unrelated {
				unrelatedBefore[id], err = s.GetJob(id)
				must(t, err)
			}
			grantID := unrelatedBefore[unrelated[0]].GrantID
			ruleID := unrelatedBefore[unrelated[1]].RuleID
			if storageFailure {
				// originalPath is an existing regular file, never a directory.
				s.path = filepath.Join(originalPath, "state.json")
				if s.DecideBatch(batch) == nil {
					t.Fatal("failed persistence acknowledged")
				}
				if stateJSON(t, s) != before {
					t.Fatal("permissions, jobs or audit escaped rollback")
				}
				after, err := os.ReadFile(originalPath)
				must(t, err)
				if string(after) != string(disk) {
					t.Fatal("failed persistence changed disk")
				}
				return
			}
			must(t, s.DecideBatch(batch))
			for _, item := range batch.Items {
				j, err := s.GetJob(item.ID)
				must(t, err)
				if j.Status != "denied" || j.Reason != "USER_DENIED" {
					t.Fatal(j)
				}
			}
			for _, id := range related {
				j, err := s.GetJob(id)
				must(t, err)
				if j.Status != "pending" || j.GrantID != "" || j.RuleID != "" {
					t.Fatal(j)
				}
			}
			for _, id := range unrelated {
				j, err := s.GetJob(id)
				must(t, err)
				if !reflect.DeepEqual(j, unrelatedBefore[id]) {
					t.Fatal("unrelated queued job changed", j)
				}
			}
			if len(s.state.Grants) != 1 || s.state.Grants[0].ID != grantID || len(s.state.Rules) != 1 || s.state.Rules[0].ID != ruleID {
				t.Fatal("denial retained matching authority or created/replaced authority")
			}
		})
	}
}
