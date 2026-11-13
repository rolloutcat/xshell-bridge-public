const fs = require('fs'), vm = require('vm');
const context = vm.createContext({});
vm.runInContext(fs.readFileSync('agent/log_parser.js', 'utf8'), context);
vm.runInContext(fs.readFileSync('tests/log_parser_cases.js', 'utf8'), context);
console.log('PASS: strict production log parser');
