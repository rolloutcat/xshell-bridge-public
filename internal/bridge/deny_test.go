package bridge

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// These are inert strings passed to an isolated Store; no shell is launched.
func TestDangerousCommandSubmission(t *testing.T) {
	for _, command := range []string{
		"rm -rf /tmp/example", "rm -fr /tmp/example", "rm -r -f /tmp/example",
		"rm --recursive --force /tmp/example", "rm /tmp/example -Rf",
		"sudo -u root /bin/rm -rf /tmp/example", "env X=1 command /usr/bin/rm -rf /tmp/example",
		"r'm' -r\"f\" /tmp/example", "r\\m -rf /tmp/example", "rm -r /", "rm -r /etc",
		"id; rm -rf /tmp/example", "id || rm -rf /tmp/example", "id\nrm -rf /tmp/example",
		"echo $(rm -rf /tmp/example)", "X=$(rm -rf /tmp/example) hostname",
		"cat <(rm -rf /tmp/example)", "echo \"${X:-$(rm -rf /tmp/example)}\"",
		"if true; then rm -rf /tmp/example; fi", "f() { rm -rf /tmp/example; }; f",
		"sh -c 'rm -rf /tmp/example'", "sudo bash -lc 'rm -rf /tmp/example'",
		"mkfs.ext4 /dev/test-disk", "mkswap /dev/test-disk", "wipefs -a /dev/test-disk", "shred /tmp/example",
		"dd if=/dev/zero of=/dev/test-disk", "echo bad > /dev/test-disk", ": <> /dev/test-disk",
		"rm -rf /tmp/example\necho '", "rm -rf \"$TARGET\"",

		"mysql -e 'DROP DATABASE example'", "mysql -e 'TRUNCATE TABLE example'",
		"mysql -e 'DELETE FROM example'", "mysql -e 'UPDATE example SET enabled=0'",
		"mysql -e 'UPDATE example SET enabled=(SELECT n FROM t WHERE id=1)'",
		"psql -c 'DROP SCHEMA example CASCADE'", "sqlite3 /tmp/test.db 'DELETE FROM example;'",
		"mysql <<'SQL'\nDROP TABLE example;\nSQL", "psql -c 'SELECT 1; TRUNCATE example;'",
		"mysql -e 'PRAGMA user_version;' -e 'DROP TABLE example'", "mysql -e 'PRAGMA user_version; DROP TABLE example'",

		"redis-cli FLUSHALL", "redis-cli -n 2 flushdb ASYNC", "redis-cli --raw CONFIG SET dir /tmp",
		"redis-cli EVAL 'return redis.call(\"FLUSHALL\")' 0",
		"mongosh --eval 'db.dropDatabase()'", "mongosh --eval 'db.users.deleteMany({})'",
		"shutdown -h now", "reboot", "systemctl poweroff", "init 0", "kill -9 -1", "killall java",
		"docker system prune -af", "docker volume rm example", "docker compose down -v",
		"kubectl delete namespace example", "kubectl delete pods --all", "terraform destroy -auto-approve",
		"chmod -R 777 /etc", "chown -R nobody /", "find /tmp -delete", "xargs rm -rf",
		"python -c 'import shutil; shutil.rmtree(\"/tmp/example\")'",
		"hostname; rm -rf /", "hostname\nreboot", "systemctl --no-pager is-active nginx.service; reboot",
		"echo ok >\"$output\"; rm -rf /tmp/example",
		"python3 -c 'exec(code); import shutil; shutil.rmtree(\"/tmp/example\")'",
		"python3 -c 'cursor.execute(\"DELETE FROM example\")'",
		"etcdctl del / --prefix", "etcdctl member remove 1234", "kafka-topics.sh --bootstrap-server localhost:9092 --delete --topic fixture",
		"kafka-consumer-groups.sh --bootstrap-server localhost:9092 --group fixture --reset-offsets --to-earliest --execute",
		"aws s3 rm s3://fixture/ --recursive", "kubectl replace --force -f /tmp/example.yaml",
	} {
		t.Run(command, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command, r.Cwd = command, "/"
			before := stateJSON(t, s)
			e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: command, Cwd: "/", TimeoutMS: r.TimeoutMS})
			must(t, err)
			if e.Decision != "blocked" || !strings.HasPrefix(e.MatchedID, "hard-deny-v1/") {
				t.Errorf("expected policy block, got %+v", e)
			}
			_, err = s.Submit(r)
			var denied *Error
			if !errors.As(err, &denied) || denied.Code != "COMMAND_DENIED" {
				t.Fatalf("expected COMMAND_DENIED, got %v", err)
			}
			if before != stateJSON(t, s) {
				t.Fatal("rejected submission or explanation mutated state")
			}
		})
	}
}

func TestDangerousCommandDoesNotMatchData(t *testing.T) {
	for _, command := range []string{
		"grep -F 'rm -rf /' /tmp/log", "printf '%s' 'rm -rf /'", "echo '$(rm -rf /)'",
		"cat <<'EOF'\nrm -rf /\nEOF", "hostname # rm -rf /",
		"rm -- -rf", "rm -f /tmp/example", "rm -r /tmp/example", "dd if=/dev/zero of=/tmp/example",
		"echo ok >/dev/null", "echo ok >&2", "cat /dev/test-disk", "bash -c 'echo ok'",
		"systemctl restart example.service",
		"mysql -e \"SELECT 'DROP TABLE example';\"", "mysql -e 'SELECT 1 /* DROP TABLE example */'",
		"mysql -e 'DELETE FROM example WHERE id=42'", "psql -c 'UPDATE example SET enabled=0 WHERE id=42'",
		"sqlite3 /tmp/test.db 'SELECT 1'", "mysql <<'SQL'\nSELECT 1;\nSQL",
		"redis-cli -n 2 GET example", "redis-cli INFO", "docker ps", "docker logs example",
		"kubectl get pods", "kill -TERM 1234", "echo 'DROP DATABASE example; FLUSHALL; reboot'",
		"timeout -k 2s 25s bash -o pipefail <<'SCAN'\npython3 - <<'PY'\nimport re,json\nprint(json.dumps({'n':len(re.findall(r'ERROR', 'fixture'))}))\nPY\nst=$?\nif [ \"$st\" -ne 0 ]; then exit \"$st\"; fi\nSCAN",
		"python3 - <<'PY'\nfrom pathlib import Path\nimport re\ns=Path('/tmp/fixture.log').read_text()\np=re.compile(r'ERROR')\nprint(len(p.findall(s)))\ncode='print(1)'\ncompile(code,'fixture.py','exec')\nPY",
		"python3 -c 'import subprocess; print(subprocess.check_output([\"systemctl\",\"show\",\"example.service\"]))'",
		"python3 -c 'print(\"shutil.rmtree(hello); DROP TABLE example\")'",
		"mongosh --eval 'db.users.find({id: 42})'",
		"etcdctl get fixture", "kafka-topics.sh --bootstrap-server localhost:9092 --list", "aws s3 ls s3://fixture/",
		"docker restart fixture", "kubectl apply -f /tmp/example.yaml", "terraform plan",
		"psql -c 'SELECT * FROM example FOR UPDATE'",
		"mysql -e 'INSERT INTO example(id) VALUES (1) ON DUPLICATE KEY UPDATE id=1'",
		"psql -c 'INSERT INTO example(id) VALUES (1) ON CONFLICT(id) DO UPDATE SET id=1'",
		"mysql -e 'CREATE TABLE child(id int, FOREIGN KEY(id) REFERENCES parent(id) ON DELETE CASCADE)'",
	} {
		t.Run(command, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command, r.Cwd = command, "/"
			j, err := s.Submit(r)
			must(t, err)
			if j.Status != "pending" && j.Status != "queued" {
				t.Fatal(j)
			}
		})
	}
}

func TestScriptNeedsFreshExactApproval(t *testing.T) {
	for _, command := range []string{
		"python3 -c 'print(1)'", "python3 -c \"$code\"", "sh -c \"$SCRIPT\"", "python3 /tmp/fixture.py",
		"python3 -c 'import base64; exec(base64.b64decode(payload))'", "psql -f /tmp/hidden.sql",
		"mysql -e \"$SQL\"", "redis-cli --pipe", "rm $FLAGS /tmp/example", "\"$PROGRAM\" /tmp/example",
		"hostname &&", "hostname\x1b[0m", "mysql -e '/*!50000 DROP TABLE example */'",
		"python3 <<'PY'\nprint(1)", "redis-cli EVAL 'return 1' 0", "redis-cli EVALSHA abc 0",
	} {
		t.Run(command, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command = command
			s.state.Grants = append(s.state.Grants, Grant{ID: ID(), SessionID: x.ID, Generation: x.Generation, Command: command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS, Expires: time.Now().Add(time.Minute)})
			j, err := s.Submit(r)
			must(t, err)
			if j.Status != "pending" {
				t.Fatal("old grant approved script", j)
			}
			e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: command, TimeoutMS: r.TimeoutMS})
			must(t, err)
			if e.Decision != "manual" || e.ReasonCode != "exact_approval_required" {
				t.Fatal(e)
			}
			before := stateJSON(t, s)
			if s.DecideRemember(j.ID, true, 30) == nil || stateJSON(t, s) != before {
				t.Fatal("script was remembered")
			}
			must(t, s.Decide(j.ID, true))
			p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
			must(t, err)
			if p.Job == nil || p.Job.ID != j.ID {
				t.Fatal("human-approved script was not dispatched")
			}
		})
	}
}

func TestDangerousCommandOverridesAuthority(t *testing.T) {
	for _, authority := range []string{"grant", "rule", "disabled-builtin"} {
		t.Run(authority, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command, r.Cwd = "rm -rf /tmp/example", "/"
			switch authority {
			case "grant":
				s.state.Grants = append(s.state.Grants, Grant{ID: ID(), SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS, Expires: time.Now().Add(time.Minute)})
			case "rule":
				_, err := s.CreateRule(RuleSpec{Name: "fixture", SessionID: x.ID, Generation: x.Generation, Cwd: "/", Program: "rm", ArgumentPatterns: []string{".*", ".*"}, MaxTimeoutMS: r.TimeoutMS, Minutes: 30})
				must(t, err)
			case "disabled-builtin":
				must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
			}
			if _, err := s.Submit(r); err == nil {
				t.Fatal("authority bypassed hard deny")
			}
			e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: "/", TimeoutMS: r.TimeoutMS})
			must(t, err)
			if e.Decision != "blocked" {
				t.Fatal(e)
			}
		})
	}
}

func TestDangerousCommandApprovalAndDispatch(t *testing.T) {
	for _, action := range []string{"approve", "remember", "batch", "dispatch"} {
		t.Run(action, func(t *testing.T) {
			s, b := batchFixture(t)
			item := &b.Items[1]
			// Simulate a task admitted by an older policy, without executing it.
			item.Command = "rm -rf /tmp/example"
			j := s.state.Jobs[item.ID]
			j.Command = item.Command
			before := stateJSON(t, s)
			switch action {
			case "approve", "remember":
				minutes := 0
				if action == "remember" {
					minutes = 30
				}
				if s.DecideRemember(j.ID, true, minutes) == nil || before != stateJSON(t, s) {
					t.Fatal("dangerous approval accepted or changed state")
				}
			case "batch":
				if s.DecideBatch(b) == nil || before != stateJSON(t, s) {
					t.Fatal("batch was not rejected atomically")
				}
			case "dispatch":
				j.Status, j.ApprovalSource = "queued", "operator"
				p, err := s.Poll(item.SessionID, item.Generation, item.Fingerprint)
				must(t, err)
				if p.Job != nil {
					t.Fatal("dangerous task dispatched")
				}
				got, err := s.GetJob(j.ID)
				must(t, err)
				if got.Status != "denied" || !strings.Contains(got.Reason, "COMMAND_DENIED") {
					t.Fatal(got)
				}
				return
			}
			// Denial remains available; the operator can clear an old pending task.
			must(t, s.Decide(j.ID, false))
		})
	}
}
