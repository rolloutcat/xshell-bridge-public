"""Real desktop Backend + real Go HTTP state machine, synthetic agent only."""
import argparse
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import time
from urllib import request, error


def run(binary, driver):
    for mode in ('Local',):
        with tempfile.TemporaryDirectory(prefix='xsh-desktop-test-') as temp:
            root=Path(temp)
            subprocess.run([binary,'init','--dir',temp],check=True,capture_output=True)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
            endpoint=f'http://127.0.0.1:{port}'
            cfg=json.loads((root/'bridge.json').read_text(encoding='utf-8'))
            cfg['listen']=f'127.0.0.1:{port}'
            (root/'bridge.json').write_text(json.dumps(cfg),encoding='utf-8')
            for role in ('admin','client','agent'):
                path=root/(role+'.json');data=json.loads(path.read_text(encoding='utf-8'));data['endpoint']=endpoint;path.write_text(json.dumps(data),encoding='utf-8')
            def api(path,body=None,role='client'):
                req=request.Request(endpoint+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+cfg['keys'][role],'Content-Type':'application/json'})
                with request.build_opener(request.ProxyHandler({})).open(req,timeout=5) as res:return json.load(res)
            server=subprocess.Popen([binary,'serve','--dir',temp],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            processes=[]
            try:
                for _ in range(100):
                    try:api('/v1/health');break
                    except error.URLError:time.sleep(.05)
                else:raise AssertionError('fixture Bridge unavailable')
                def start(args):
                    p=subprocess.Popen([driver]+args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8');processes.append(p);return p
                operator=start(['--mode',mode,'--dir',temp])
                seq=0
                def ipc(p,op,**extra):
                    nonlocal seq
                    seq+=1;p.stdin.write(json.dumps(dict(id=seq,op=op,**extra))+'\n');p.stdin.flush()
                    line=p.stdout.readline();assert line,'native fixture exited';r=json.loads(line);assert r['id']==seq;return r
                def ok(op,**extra):
                    r=ipc(operator,op,**extra);assert r['ok'],r;return r['data']
                def capture(ids):
                    snapshot=ok('snapshot')
                    jobs={j['id']:j for j in snapshot['jobs']}
                    sessions={s['id']:s for s in snapshot['sessions']}
                    return dict(context=snapshot['approval_context'],items=[dict(job=jobs[i],session=sessions[jobs[i]['session_id']]) for i in ids])
                def displayed(action,shown,remember=False,success=True):
                    r=ipc(operator,'displayedDecide',action=action,remember=remember,**shown)
                    assert r['ok']==success,r
                    return r
                def decide(action,target,remember=False):
                    if action=='activate':
                        review=ok('prepare',action=action,target=target)
                        ok('confirm',ticket=review['ticket'],remember=False)
                        assert not ipc(operator,'confirm',ticket=review['ticket'])['ok']
                    else:
                        shown=capture([target]);displayed(action,shown,remember)
                        displayed(action,shown,remember,success=False)
                s=api('/v1/agent/register',dict(instance='desktop-fixture-'+mode,name='Desktop fixture '+mode,environment='ISOLATED TEST ONLY',fingerprint='synthetic-terminal-'+mode,auto_name=True),'agent')
                assert ok('snapshot')['sessions'][0]['state']=='pending'
                assert s['name']=='终端 01' and ok('snapshot')['supports_labels']
                ok('labelSession',target=s['id'],generation=s['generation'],name='业务服务器·终端1',environment='测试环境')
                current=api('/v1/sessions')[0]
                assert current['name']=='业务服务器·终端1' and current['environment']=='测试环境' and current['state']=='pending'
                assert current['fingerprint']==s['fingerprint'] and current['generation']==s['generation']
                decide('activate',s['id'])
                def submit(rid):return api('/v1/jobs',dict(session_id=s['id'],generation=s['generation'],request_id=rid,command='fixture status check',cwd='/',timeout_ms=15000))
                denied=submit('denied');decide('deny',denied['id']);assert api('/v1/job?id='+denied['id'])['status']=='denied'
                j=submit('approved');decide('allow',j['id'],True)
                assert api('/v1/job?id='+j['id'])['status']=='queued'
                poll=dict(id=s['id'],generation=s['generation'],fingerprint=s['fingerprint'])
                assert api('/v1/agent/poll',poll,'agent')['job']['id']==j['id']
                assert next(x for x in ok('snapshot')['jobs'] if x['id']==j['id'])['status']=='dispatched'
                event=dict(session_id=s['id'],generation=s['generation'],fingerprint=s['fingerprint'],job_id=j['id'],seq=1,output='hello 中文\n',status='running')
                api('/v1/agent/event',event,'agent');out=ok('output',target=j['id'],cursor=0);assert out['output']=='hello 中文\n' and out['status']=='running'
                event.update(seq=2,output='done\n',status='completed',exit_code=7);api('/v1/agent/event',event,'agent')
                out=ok('output',target=j['id'],cursor=out['next_cursor']);assert out['output']=='done\n' and out['exit_code']==7
                auto=submit('auto');assert auto['status']=='queued' and auto['grant_id']
                ok('revoke',target=auto['grant_id']);assert api('/v1/job?id='+auto['id'])['status']=='pending'
                shown=capture([auto['id']])
                api('/v1/detach',dict(id=s['id'],generation=s['generation']))
                displayed('allow',shown,success=False)
                assert api('/v1/job?id='+auto['id'])['status']=='cancelled'
                assert not ok('snapshot')['grants']
                # New scopes and resolution use this isolated Bridge only; no Xshell/network target.
                second=api('/v1/agent/register',dict(instance='rule-fixture-session-'+mode,name='Production Alpha',environment='isolated',fingerprint='rule-terminal-'+mode),'agent')
                decide('activate',second['id'])
                def resolve(name,fuzzy=False):
                    args=[binary,'--config',str(root/'client.json'),'resolve','session','--name',name]
                    if fuzzy:args.append('--fuzzy')
                    result=subprocess.run(args,capture_output=True,text=True,encoding='utf-8',timeout=10)
                    return result.returncode,json.loads(result.stdout)
                code,resolved=resolve('Production Alpha');assert code==0 and resolved['candidates'][0]['id']==second['id']
                ok('labelSession',target=second['id'],generation=second['generation'],name='Renamed Alpha',environment='isolated')
                assert resolve('Production Alpha')[0]==2
                code,resolved=resolve('alpha',True);assert code==2 and resolved['status']=='suggestions' and resolved['requires_selection']
                assert resolve('Renamed Alpha')[0]==0
                # Default rules cover both df -h and df -i; use an explicit path
                # for the custom-rule fixture so it still tests that authority.
                builtin=api('/v1/jobs',dict(session_id=second['id'],generation=second['generation'],request_id='builtin-default',command='df -h',cwd='/',timeout_ms=15000))
                assert builtin['status']=='queued' and builtin['approval_source']=='builtin'
                api('/v1/interrupt',dict(id=builtin['id']))
                spec=dict(name='inode fixture',session_id=second['id'],generation=second['generation'],program='df',argument_patterns=['-i','/'],cwd='/',max_timeout_ms=15000,minutes=30)
                preview=ok('rulePrepare',spec=spec);assert not api('/v1/rules')
                ok('ruleConfirm',ticket=preview['ticket']);assert not ipc(operator,'ruleConfirm',ticket=preview['ticket'])['ok']
                rule=api('/v1/rules')[0]
                job=api('/v1/jobs',dict(session_id=second['id'],generation=second['generation'],request_id='rule-match',command='df -i /',cwd='/',timeout_ms=15000))
                assert job['status']=='queued' and job['rule_id']==rule['id']
                ok('ruleRevoke',target=rule['id']);assert api('/v1/job?id='+job['id'])['status']=='pending'
                decide('deny',job['id'])
                # Two isolated targets exercise the actual native batch adapter and Go atomic endpoint.
                third=api('/v1/agent/register',dict(instance='batch-fixture-'+mode,name='Batch fixture',environment='isolated',fingerprint='batch-terminal-'+mode),'agent')
                decide('activate',third['id'])
                def batch_job(target,rid):
                    return api('/v1/jobs',dict(session_id=target['id'],generation=target['generation'],request_id=rid,command='printf batch-fixture',cwd='/fixture',timeout_ms=15000))
                pair=[batch_job(second,'batch-a'),batch_job(third,'batch-b')]
                ids=[x['id'] for x in pair]
                before=[api('/v1/job?id='+x) for x in ids]
                audit=api('/v1/audit')
                for p in (operator,):
                    explained=ipc(p,'explainJob',target=ids[0]);assert explained['ok'],explained
                    assert explained['data']['explanation']['decision']=='manual'
                assert [api('/v1/job?id='+x) for x in ids]==before and api('/v1/audit')==audit
                shown=capture(ids)
                for item,expected in zip(shown['items'],before):
                    assert all(item['job'][k]==expected[k] for k in ('id','session_id','generation','command','cwd','timeout_ms'))
                    assert all(k in item['session'] for k in ('id','generation','fingerprint','name','environment'))
                assert [api('/v1/job?id='+x) for x in ids]==before
                displayed('allow',shown)
                assert all(api('/v1/job?id='+x)['status']=='queued' for x in ids)
                audit=api('/v1/audit')
                displayed('allow',shown,success=False)
                assert api('/v1/audit')==audit
                for x in ids:api('/v1/interrupt',dict(id=x))
                stale=[batch_job(second,'stale-a'),batch_job(third,'stale-b')]
                stale_ids=[x['id'] for x in stale]
                shown=capture(stale_ids)
                # Change through HTTP, not Backend: the displayed snapshot must be rejected atomically.
                api('/v1/admin/session-label',dict(id=third['id'],generation=third['generation'],name='Changed batch fixture',environment='isolated'),'admin')
                audit=api('/v1/audit')
                displayed('allow',shown,success=False)
                assert all(api('/v1/job?id='+x)['status']=='pending' for x in stale_ids)
                assert api('/v1/audit')==audit
                # Rule creation affects new submissions only; denial revokes matching policy.
                batch_spec=dict(name='batch printf',session_id=second['id'],generation=second['generation'],program='printf',argument_patterns=['batch-fixture'],cwd='/fixture',max_timeout_ms=15000,minutes=30)
                rule_preview=ok('rulePrepare',spec=batch_spec)
                ok('ruleConfirm',ticket=rule_preview['ticket'])
                queued=batch_job(second,'batch-rule-match');assert queued['status']=='queued'
                assert api('/v1/job?id='+stale_ids[0])['status']=='pending'
                shown=capture(stale_ids)
                displayed('deny',shown)
                assert all(api('/v1/job?id='+x)['status']=='denied' for x in stale_ids)
                assert api('/v1/job?id='+queued['id'])['status']=='pending'
                assert not api('/v1/rules')
                audit=api('/v1/audit')
                displayed('deny',shown,success=False)
                assert api('/v1/audit')==audit
                api('/v1/interrupt',dict(id=queued['id']))
                api('/v1/detach',dict(id=third['id'],generation=third['generation']))
                assert api('/v1/health')['safe_shutdown']
                try:api('/v1/admin/shutdown',{},'admin');raise AssertionError('busy shutdown accepted')
                except error.HTTPError as e:assert e.code==409
                api('/v1/detach',dict(id=second['id'],generation=second['generation']))
                api('/v1/admin/shutdown',{},'admin');assert server.wait(timeout=10)==0
                assert not (root/'bridge.lock').exists()
                print('PASS:',mode,'native IPC + real Go: approvals/output, rule lifecycle, rename/exact/fuzzy resolution, idle shutdown and lock removal')
            finally:
                for p in processes:
                    if p.poll() is None:p.stdin.close();p.wait(timeout=15)
                    err=p.stderr.read();assert not err,err
                server.terminate();server.wait(timeout=10)

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--binary',required=True);parser.add_argument('--driver',required=True);args=parser.parse_args()
    run(str(Path(args.binary).resolve()),str(Path(args.driver).resolve()))
