// Shared ES3 strict log parser tests.
(function () {
    function assert(ok, message) { if (!ok) { throw new Error(message); } }
    function bytes(text) {
        var s = encodeURIComponent(text), out = [], i = 0;
        while (i < s.length) {
            if (s.charAt(i) === '%') { out.push(parseInt(s.substr(i + 1, 2), 16)); i += 3; }
            else { out.push(s.charCodeAt(i++)); }
        }
        return out;
    }
    function rejected(fn, label) {
        var e = null; try { fn(); } catch (caught) { e = caught; }
        assert(e !== null, 'accepted ' + label);
    }
    var transcript = '\ufeffecho __XSH_BEGIN_abc-extra\r\n' +
        '__XSH_BEGIN_abc\r\n\r\nsame\r\nsame\n' + new Array(101).join('x') +
        '\n\u4e2d\ud83d\ude42\nwithout final newline\n__XSH_END_abc:7\r\nprompt\n';
    var expected = '\nsame\nsame\n' + new Array(101).join('x') + '\n\u4e2d\ud83d\ude42\nwithout final newline\n';
    var data = bytes(transcript), i, p, out, result;
    for (i = 0; i <= data.length; i++) {
        out = ''; p = new LogFrames('abc', function (s) { out += s; });
        p.feed(data.slice(0, i), false); p.feed(data.slice(i), true);
        assert(out === expected && p.code === 7, 'split at ' + i);
    }
    out = ''; p = new LogFrames('abc', function (s) { out += s; });
    for (i = 0; i < data.length; i++) { p.feed([data[i]], false); }
    p.feed([], true);
    assert(out === expected && p.code === 7, 'one byte chunks');
    out = ''; p = new LogFrames('a', function (s) { out += s; });
    p.feed(bytes('__XSH_BEGIN_a\n' + new Array(10001).join('x') + '\n__XSH_END_a:0\n'), true);
    assert(out.length === 10001 && p.code === 0, 'long line');
    p = new LogFrames('abc', function () {});
    p.feed(bytes('__XSH_BEGIN_abc\n__XSH_END_abc:0'), false);
    assert(p.code === null, 'partial completion accepted');
    rejected(function () { p.feed([], true); }, 'unterminated line');
    p = new LogFrames('abc', function () {});
    p.feed(bytes('__XSH_BEGIN_abc\n__XSH_END_abc:0\n'), false);
    assert(p.code === null, 'completion exposed before final read');
    p.feed([], true);
    assert(p.code === 0, 'finalized completion unavailable');
    out = ''; p = new LogFrames('abc', function (s) { out += s; });
    p.feed(bytes('__XSH_BEGIN_abc\nresult\n__XSH_END_abc:7\n__XSH_DONE_abc\nuser@host $ '), true);
    assert(p.code === 7 && out === 'result\n', 'non-newline shell prompt after complete END must not invalidate or enter output');
    p = new LogFrames('abc', function () {});
    rejected(function () { p.feed(bytes('__XSH_BEGIN_abc\n__XSH_END_abc:0\n').concat([0xff]), true); }, 'invalid data after end');
    assert(p.code === null, 'completion exposed after invalid trailing data');
    rejected(function () { p.feed([], true); }, 'reuse after parser error');
    assert(p.code === null, 'error recovery exposed completion');
    var invalid = [[0xc0,0x80], [0xed,0xa0,0x80], [0xf4,0x90,0x80,0x80], [0xe4,0xb8]];
    for (i = 0; i < invalid.length; i++) {
        (function (v) { rejected(function () { new LogFrames('a', function () {}).feed(v, true); }, 'invalid UTF-8'); })(invalid[i]);
    }
    rejected(function () { new LogFrames('a', function () {}).feed(bytes('__XSH_BEGIN_a\n\u001b[31mtext\n'), true); }, 'ANSI');
    rejected(function () { new LogFrames('a', function () {}).feed(bytes('__XSH_BEGIN_a\n__XSH_END_a:0suffix\n'), true); }, 'spoofed end');
    out = ''; p = new LogFrames('a', function (s) { out += s; });
    p.feed(bytes('__XSH_BEGIN_a\nconfirmed\n'), false);
    rejected(function () { p.feed([0xff], true); }, 'bad byte after output');
    assert(out === 'confirmed\n', 'confirmed output lost');
    p = new LogFrames('a', function () {}, 4);
    rejected(function () { p.feed([1,2,3,4,5], false); }, 'size limit');
    var left = '', right = '', one = new LogFrames('one', function (s) { left += s; });
    var two = new LogFrames('two', function (s) { right += s; });
    one.feed(bytes('__XSH_BEGIN_one\none\n__XSH_END_one:1\n'), true);
    two.feed(bytes('__XSH_BEGIN_two\ntwo\n__XSH_END_two:2\n'), true);
    assert(left === 'one\n' && right === 'two\n' && one.code === 1 && two.code === 2, 'independent parsers');
    p = new LogFrames('a', function () {});
    p.feed(bytes('[12:00] __XSH_BEGIN_a\n[12:00] __XSH_END_a:0\n'), true);
    assert(!p.started && p.code === null, 'timestamp prefix cannot forge frame');
}());
