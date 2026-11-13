// This is registered-agent status, not a scan of Xshell processes or terminal tabs.
function attachmentStatus(sessions, connected) {
    if (!connected) return {kind:'unavailable',title:'暂时无法检测脚本接入',detail:'请先恢复 Bridge 连接；跨机使用时也检查隧道。此时不能判断 Xshell 脚本是否运行。'};
    const active=sessions.filter(s=>s.state==='active').length;
    const pending=sessions.filter(s=>s.state==='pending').length;
    if (pending) return {kind:'pending',title:`已检测到 ${pending} 个待激活接入`,detail:'脚本已注册，无需重复运行。请在 Bridge 所在机器核对目标并激活。'};
    if (active) return {kind:'active',title:`已检测到 ${active} 个有效接入`,detail:'当前接入可持续处理任务，无需逐条运行脚本；其他服务器需要分别接入。'};
    return {kind:'missing',title:'未检测到有效脚本接入',detail:sessions.length?'历史接入已失效或被撤销。确认旧任务状态、到达最终目标后，请在 Xshell 重新运行接入脚本。':'请先在远程 Xshell 页面连接并导出脚本，在每个最终目标终端运行一次，然后返回本机核对并激活。'};
}
if(typeof module!=='undefined')module.exports={attachmentStatus};
