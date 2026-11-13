"""Local fixed diagnostic subprocess tests. No Bridge, configs, SSH or Xshell."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import time
import uuid

p=argparse.ArgumentParser();p.add_argument('--binary',required=True);p.add_argument('--native-tests',required=True);a=p.parse_args()
root=Path(__file__).resolve().parents[1]
stage=root/'dist'/('process-diagnostic-'+uuid.uuid4().hex);stage.mkdir()
csc=Path(os.environ['WINDIR'])/'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
env=dict(os.environ,XSHCTL_CONFIG=str(stage/'nonexistent-config.json'))
# Actual freshly built CLI succeeds with deliberately missing configuration, offline.
result=subprocess.run([str(Path(a.binary).resolve()),'processes'],env=env,capture_output=True,timeout=8,encoding='utf-8',check=True)
report=json.loads(result.stdout);assert report['available']
assert any(row['role']=='cli' for row in report['processes'])
(stage/'actual-cli-report.json').write_text(result.stdout,encoding='utf-8')
def native(folder,expected):
    driver=folder/'BackendTests.exe';shutil.copy2(a.native_tests,driver)
    started=time.monotonic()
    result=subprocess.run([str(driver),'--process-diagnostic'],capture_output=True,timeout=10,encoding='utf-8',check=True,env=env)
    response=json.loads(result.stdout);assert response['ok']==expected,response
    assert time.monotonic()-started<8,'native diagnostic exceeded budget'
    return response
actual=stage/'actual';actual.mkdir();shutil.copy2(a.binary,actual/'xshctl.exe')
response=native(actual,True);assert response['data']['available']
assert any(row['role']=='cli' for row in response['data']['processes'])
for name,body in {
    'malformed':'Console.Write("not json");',
    'overflow':"Console.Write(new string('x',200000));",
    'stderr':'Console.Error.Write("hidden fixture error");',
    'timeout':'Thread.Sleep(30000);',
    'exit-code':'Environment.Exit(7);',
}.items():
    folder=stage/name;folder.mkdir();source=folder/'fixture.cs'
    source.write_text('using System;using System.Threading;class Fixture {static void Main(string[] args){if(args.Length!=1||args[0]!="processes")Environment.Exit(22);'+body+'}}',encoding='utf-8')
    subprocess.run([str(csc),'/nologo','/target:exe','/out:'+str(folder/'xshctl.exe'),str(source)],check=True,capture_output=True)
    response=native(folder,False);assert 'hidden fixture error' not in json.dumps(response)
print('PASS: actual CLI without config; actual offline readonly native subprocess; malformed/oversize/stderr/nonzero/5s-timeout rejection. Fixtures retained:',stage)
