function assert(value, message) { if (!value) { throw new Error(message); } }
function rejects(fn) { var failed=false; try { fn(); } catch(e) { failed=true; } assert(failed,"expected rejection"); }
rejects(function(){ parseJSON('{"x":1,"x":2}'); });
rejects(function(){ parseJSON('{"__proto__":{}}'); });
rejects(function(){ parseJSON('{"x":(function(){throw 1;})()}'); });
rejects(function(){ parseJSON('[1,]'); });
rejects(function(){ parseJSON('01'); });
assert(parseJSON(stringifyJSON({s:'a\n"\\\u4e2d', a:[true,null,7]})).s === 'a\n"\\\u4e2d', 'JSON roundtrip');
rejects(function(){ new BridgeAPI({endpoint:'http://evil.invalid',token:'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}); });
rejects(function(){ new BridgeAPI({endpoint:'http://127.0.0.1:18765/evil',token:'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}); });
rejects(function(){ new BridgeAPI({endpoint:'http://127.0.0.1:18765',token:'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}).post('/v1/admin/activate',{}); });
var unicodeText = new Array(4000).join('a') + '\ud83d\ude42tail';
var unicodeChunks = chunks(unicodeText);
assert(unicodeChunks.join('') === unicodeText && unicodeChunks[0].length === 3999,'surrogate pair split');

WScript.Echo('PASS: JSON, loopback, Unicode chunks');
