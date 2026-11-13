// ES3 strict UTF-8 terminal transcript framing, shared by exports and tests.
function LogFrames(nonce, emit, limit) {
    if (!/^[A-Za-z0-9_-]+$/.test(nonce) || typeof emit !== 'function') { throw new Error('invalid log parser arguments'); }
    this.begin = '__XSH_BEGIN_' + nonce;
    this.end = '__XSH_END_' + nonce + ':';
    this.emit = emit; this.limit = typeof limit === 'number' ? limit : 8 * 1024 * 1024;
    this.size = 0; this.line = ''; this.cr = false; this.initial = true;
    this.remaining = 0; this.point = 0; this.minimum = 0;
    this.started = false; this.ended = false; this.code = null; this.pendingCode = null; this.closed = false; this.failed = false;
}
LogFrames.prototype.character = function (point) {
    if (this.initial) { this.initial = false; if (point === 0xfeff) { return; } }
    if (this.cr) {
        if (point !== 10) { throw new Error('unsupported carriage return in log'); }
        this.cr = false;
    } else if (point === 13) { this.cr = true; return; }
    if (point === 10) {
        var line = this.line; this.line = '';
        if (this.ended) { return; }
        if (!this.started) { if (line === this.begin) { this.started = true; } return; }
        if (line.substr(0, this.end.length) === this.end) {
            var code = line.substr(this.end.length);
            if (!/^[0-9]{1,3}$/.test(code) || Number(code) > 255) { throw new Error('invalid completion marker'); }
            this.ended = true; this.pendingCode = Number(code); return;
        }
        this.emit(line + '\n'); return;
    }
    if ((point < 32 && point !== 9) || (point >= 127 && point <= 159)) { throw new Error('unsupported control character in log'); }
    if (point <= 0xffff) { this.line += String.fromCharCode(point); }
    else { point -= 0x10000; this.line += String.fromCharCode(0xd800 + (point >> 10), 0xdc00 + (point & 1023)); }
};
LogFrames.prototype.feed = function (bytes, final) {
    if (this.failed) { throw new Error('log parser previously failed'); }
    try { this.readBytes(bytes, final); } catch (e) { this.failed = true; this.code = null; throw e; }
};
LogFrames.prototype.readBytes = function (bytes, final) {
    if (this.closed) { throw new Error('log parser already closed'); }
    if (this.size + bytes.length > this.limit) { throw new Error('log size limit exceeded'); }
    this.size += bytes.length;
    for (var i = 0, b, point; i < bytes.length; i++) {
        b = bytes[i];
        if (typeof b !== 'number' || b < 0 || b > 255 || Math.floor(b) !== b) { throw new Error('invalid log byte'); }
        if (this.remaining) {
            if (b < 0x80 || b > 0xbf) { throw new Error('invalid UTF-8 continuation'); }
            this.point = this.point * 64 + (b & 63); this.remaining--;
            if (!this.remaining) {
                point = this.point;
                if (point < this.minimum || point > 0x10ffff || (point >= 0xd800 && point <= 0xdfff)) { throw new Error('invalid UTF-8 code point'); }
                this.character(point);
            }
        } else if (b < 0x80) { this.character(b); }
        else if (b >= 0xc2 && b <= 0xdf) { this.remaining = 1; this.point = b & 31; this.minimum = 0x80; }
        else if (b >= 0xe0 && b <= 0xef) { this.remaining = 2; this.point = b & 15; this.minimum = 0x800; }
        else if (b >= 0xf0 && b <= 0xf4) { this.remaining = 3; this.point = b & 7; this.minimum = 0x10000; }
        else { throw new Error('invalid UTF-8 lead byte'); }
    }
    if (final) {
        // A complete END closes the result frame. A later shell prompt normally
        // has no newline; still validate every byte, but do not require its LF.
        if (this.remaining || this.cr || (!this.ended && this.line.length)) { throw new Error('incomplete log data'); }
        this.closed = true; this.code = this.pendingCode;
    }
};
