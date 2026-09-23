'use strict';

const token = new URLSearchParams(location.hash.slice(1)).get('bridge_token');
const pending = new Map();
const $ = id => document.getElementById(id);
let sequence = 0;
let saved = {};
let loaded = false;
let busy = false;
let resizeFrame = 0;
let verificationTimer = 0;

function resize() {
  if (resizeFrame) return;
  resizeFrame = requestAnimationFrame(() => {
    resizeFrame = 0;
    if (token && parent !== window) parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type: 'ui.resize', height: document.body.scrollHeight }, '*');
  });
}
function send(type, extra = {}) {
  if (!token || parent === window) return Promise.reject(new Error('请从宿主插件配置页打开此界面'));
  const requestId = String(++sequence);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(requestId); reject(new Error('宿主响应超时，请稍后重试')); }, 30000);
    pending.set(requestId, { resolve, reject, timer });
    parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: token, type, request_id: requestId, ...extra }, '*');
  });
}
window.addEventListener('message', event => {
  const data = event.data;
  if (event.source !== parent || !data || data.source !== 'sub2api-plugin-host' || data.bridge_token !== token) return;
  const request = pending.get(data.request_id);
  if (!request) return;
  pending.delete(data.request_id);
  clearTimeout(request.timer);
  data.ok ? request.resolve(data) : request.reject(new Error(data.error || '宿主未能完成操作'));
});
window.addEventListener('pagehide', () => {
  clearTimeout(verificationTimer);
  for (const request of pending.values()) { clearTimeout(request.timer); request.reject(new Error('页面已关闭')); }
  pending.clear();
  observer?.disconnect();
  if (resizeFrame) cancelAnimationFrame(resizeFrame);
});

const errorText = {
  SUBSCRIPTION_DOWNLOAD_FAILED: '订阅下载失败。请检查地址是否有效，以及内核所在机器的网络。',
  SUBSCRIPTION_INVALID: '订阅内容不能解析为受支持节点，请检查链接或订阅格式。',
  SUBSCRIPTION_HTTP_ERROR: '订阅服务器拒绝请求，请检查链接有效期或下载标识。',
  SUBSCRIPTION_FILE_ERROR: '订阅文件无法读取，请检查内核所在机器的文件路径。',
  SUBSCRIPTION_TOO_LARGE: '订阅超过 2 MiB 上限。',
  SUBSCRIPTION_EMPTY: '订阅没有提供可导入节点。',
  PROXY_ENV_EMPTY: '代理环境变量没有值。请改填完整代理地址，或配置插件进程的环境。',
  SUBSCRIPTION_ENV_EMPTY: '订阅环境变量没有值。请改填订阅链接，或配置插件进程的环境。',
  SOURCE_TIMEOUT: '获取来源超时，请检查网络后重试。',
  SOURCE_CANCELLED: '获取来源已取消，请重试。',
  SOURCE_INVALID: '来源配置无效，请检查地址、端口或变量名。',
  SOURCE_UNAVAILABLE: '来源无法读取。环境变量和文件必须位于内核所在机器。',
  NO_NODES: '来源没有提供节点，或所有节点被过滤。',
  ROUTE_NOT_FOUND: '所选节点已不在订阅中，请重新获取节点并选择。',
  ROUTE_UNAVAILABLE: '所选节点已不在订阅中，请从列表重新选择。',
  ROUTE_SOURCE_INVALID: '代理来源无效，请检查协议、主机和端口。',
  ROUTE_LIMIT_EXCEEDED: '合并后的节点超过 256 个，请添加筛选条件。',
  NO_AVAILABLE_ROUTES: '本轮没有节点连通目标，请查看逐节点结果。',
  TEST_TIMEOUT: '本轮测试超时，未完成的节点可单独重试。',
  TEST_CANCELLED: '本轮测试已取消，已完成的结果保留。',
};
function humanError(error) {
  const message = String(error?.message || error || '操作失败');
  if (message.toLowerCase() === 'internal error') return '宿主读取配置失败。原配置尚未加载，请在宿主处理配置恢复后重新读取。';
  if (message.includes('Unknown')) return '宿主拒绝了配置，请检查字段范围或重新读取配置。';
  return message;
}
function setStatus(message, failed = false) {
  $('status').textContent = message;
  $('status').dataset.error = String(failed);
  resize();
}
function lines(raw) { return String(raw || '').split(/\r?\n/).map(s => s.trim()).filter(Boolean); }
function normalizeProxyUrl(raw) {
  const value = raw.trim().replace(/^(https?|socks5h?):(?=[^/])/i, '$1://');
  let url;
  try { url = new URL(value); } catch { throw new Error('代理地址无效，请填写协议、主机和端口'); }
  if (!['http:', 'https:', 'socks5:', 'socks5h:'].includes(url.protocol) || !url.hostname || url.hash || url.search || (url.pathname && url.pathname !== '/')) throw new Error('代理需使用 HTTP、HTTPS、SOCKS5 或 SOCKS5H 地址');
  return url.toString().replace(/\/$/, '');
}
function sourceKey(source) { return source.url ? 'url:' + source.url : source.url_env ? 'env:' + source.url_env : 'file:' + source.file; }
function displaySubscriptions(sources) {
  return (sources || []).map(s => s.url || (s.url_env ? 'env:' + s.url_env : 'file:' + s.file)).join('\n');
}
function readSubscriptions(raw) {
  const old = new Map((saved.subscriptions || []).map(s => [sourceKey(s), s]));
  const values = lines(raw).map((value, i) => {
    let source;
    if (value.startsWith('env:')) source = { url_env: value.slice(4).trim() };
    else if (value.startsWith('file:')) source = { file: value.slice(5).trim() };
    else {
      let url;
      try { url = new URL(value); } catch { throw new Error('第 ' + (i+1) + ' 条订阅地址无效，请粘贴完整链接（不要插入换行）'); }
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new Error('第 ' + (i+1) + ' 条订阅需使用 HTTPS 地址');
      source = { url: value };
    }
    if (!Object.values(source)[0]) throw new Error('第 ' + (i+1) + ' 条订阅来源为空');
    return { ...(old.get(sourceKey(source)) || {}), ...source };
  });
  if (values.length > 16) throw new Error('最多支持 16 条订阅来源');
  return values;
}
function updateButtons() {
  document.querySelectorAll('[data-command], #save-json').forEach(el => { el.disabled = busy || !loaded; });
  document.querySelectorAll('[data-core-blocked="true"]').forEach(el => { el.disabled = true; });
  $('reload').disabled = busy;
  $('fixed-route-id').disabled = $('route-mode').value !== 'fixed';
  $('egress-route').disabled = $('egress-mode').value !== 'fixed';
  $('length').disabled = $('account-mode').value !== 'custom';
  $('nodes').setAttribute('aria-busy', String(busy));
  $('state-sessions').setAttribute('aria-busy', String(busy));
}
function renderRouteChoices(id, nodes, selectedID, placeholder = '请选择节点') {
  const choices = $(id);
  choices.replaceChildren(new Option(placeholder, ''));
  for (const node of nodes) choices.add(new Option((node.name || node.protocol || '节点') + ' · ' + node.id, node.id));
  if (selectedID && !nodes.some(n => n.id === selectedID)) choices.add(new Option('已保存节点（需重新获取） · ' + selectedID, selectedID));
  choices.value = selectedID || '';
}
function renderNodes(report, selectedID) {
  const nodes = report?.schema === 1 && Array.isArray(report.nodes) ? report.nodes : [];
  const body = $('nodes-body');
  body.replaceChildren();
  renderRouteChoices('fixed-route-id', nodes, selectedID);
  renderRouteChoices('egress-route', nodes, saved.egress_route);
  const retryID = $('retry-route').value;
  renderRouteChoices('retry-route', nodes, nodes.some(node => node.id === retryID) ? retryID : '', '自动选择采集节点');
  if (!nodes.length) {
    const cell = document.createElement('td'); cell.colSpan = 3; cell.textContent = '尚未获取节点列表';
    const row = document.createElement('tr'); row.append(cell); body.append(row);
  }
  const statusLabels = { untested: '未测试', available: '已连通', restricted: '已连通 · 目标限制', unavailable: '连接失败', cancelled: '未完成 · 请重试' };
  const reasons = { proxy_auth: '代理认证失败', timeout: '连接超时', tls: 'TLS 校验失败', cancelled: '本轮超时', connect: '连接失败', target: '目标响应异常', http_restricted: '目标限制访问' };
  for (const node of nodes) {
    const row = document.createElement('tr'); row.dataset.nodeId = node.id;
    const name = document.createElement('td');
    const label = document.createElement('strong'); label.textContent = node.name || '代理节点';
    const protocol = document.createElement('small'); protocol.textContent = String(node.protocol || '').toUpperCase() + ' · ' + node.id;
    name.append(label, protocol);
    const status = document.createElement('td'); status.dataset.state = node.status;
    status.textContent = statusLabels[node.status] || '未测试';
    const detail = document.createElement('small');
    detail.textContent = [node.latency_ms ? node.latency_ms + ' ms' : '', node.http_status ? 'HTTP ' + node.http_status : '', reasons[node.error_code] || ''].filter(Boolean).join(' · ');
    status.append(detail);
    const actions = document.createElement('td');
    for (const [action, title] of [['test', '测试连通性'], ['select', selectedID === node.id && $('route-mode').value === 'fixed' ? '已选择' : '使用此节点']]) {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'button button-secondary'; button.textContent = title;
      button.dataset.action = action; button.dataset.nodeId = node.id; button.dataset.command = '';
      actions.append(button);
    }
    row.append(name, status, actions); body.append(row);
  }
  const counts = { available: 0, restricted: 0, unavailable: 0, pending: 0 };
  nodes.forEach(n => { if (n.status in counts) counts[n.status]++; else counts.pending++; });
  const summary = nodes.length ? nodes.length + ' 个节点 · 已连通 ' + counts.available + ' · 目标限制 ' + counts.restricted + ' · 失败 ' + counts.unavailable + ' · 未测试/未完成 ' + counts.pending : '获取节点后，可逐个测试并选择出口。';
  $('nodes-status').textContent = report?.error_code ? (errorText[report.error_code] || '节点操作失败，请检查来源后重试') : summary;
  $('nodes-status').dataset.error = String(!!report?.error_code);
  updateButtons();
}
function durationLabel(seconds) {
  const value = Math.max(0, Number(seconds) || 0);
  if (value < 60) return Math.floor(value) + ' 秒';
  return Math.floor(value / 60) + ' 分 ' + Math.floor(value % 60) + ' 秒';
}
function cellText(primary, detail = '') {
  const cell = document.createElement('td');
  const label = document.createElement('strong'); label.textContent = primary;
  cell.append(label);
  if (detail) { const small = document.createElement('small'); small.textContent = detail; cell.append(small); }
  return cell;
}
function tableEmpty(body, message) {
  body.replaceChildren();
  const row = document.createElement('tr'); const cell = document.createElement('td');
  cell.colSpan = 3; cell.textContent = message; row.append(cell); body.append(row);
}
const activeVerificationStates = new Set(['pending', 'running', 'cooling_down']);
function renderVerification(report, sessions) {
  const selector = $('verification-session');
  const previous = selector.value;
  selector.replaceChildren(new Option('请选择已建立的运行会话', ''));
  for (const session of sessions) selector.add(new Option('账号 ' + session.account_id + ' · ' + session.model, session.id));
  selector.value = sessions.some(s => s.id === previous) ? previous : (sessions[0]?.id || '');
  const session = sessions.find(s => s.id === selector.value);
  const jobs = Array.isArray(report?.node_verifications) ? report.node_verifications : [];
  const job = jobs.find(j => j.session_id === selector.value);
  const active = !!job && activeVerificationStates.has(job.state);
  const paused = !session || [401,403,429].includes(session.rejected_status);
  $('verify-all-states').dataset.coreBlocked = String(paused || active);
  $('verify-all-states').title = paused ? '先建立运行会话并等待上游暂停解除' : session.cooldown_seconds > 0 ? '可先加入验证队列，冷却结束后才会发出请求' : '向此会话的上游逐节点发出短请求，按上限和冷却分批执行';
  $('verify-one-state').dataset.coreBlocked = String(paused || active || !$('retry-route').value);
  $('cancel-verification').dataset.coreBlocked = String(!active);
  selector.disabled = !sessions.length;
  const knownCount = (saved.route_report?.nodes || []).length;
  $('verify-all-states').textContent = '验证全部节点的 state' + (knownCount ? '（最多 ' + knownCount + ' 次短请求）' : '（消耗额度）');
  const states = {pending:'准备中',running:'正在验证',cooling_down:'轮次冷却中',completed:'验证完成',paused:'上游要求暂停',cancelled:'已取消'};
  const body = $('verification-body');
  if (!job) {
    $('verification-status').textContent = session ? '尚未验证此会话的节点。连通性结果不代表能够取得合格 state。' : '暂无可用会话。先允许普通转发并发送一次 OAuth 请求，再刷新运行状态。';
    $('verification-progress').textContent = '验证会消耗所选账号额度；不显示或保存完整请求头内容。';
    tableEmpty(body, '尚未验证，不以连通性结果推测 state 资格');
  } else {
    $('verification-status').textContent = (states[job.state] || job.state) + (job.reason ? ' · ' + job.reason : '');
    let next = '';
    if (job.state === 'cooling_down' && job.next_run_at) { const date = new Date(job.next_run_at); if (!Number.isNaN(date.getTime())) next = ' · 下轮最早 ' + date.toLocaleTimeString('zh-CN', {hour12:false}); }
    const rows = Array.isArray(job.rows) ? job.rows : [];
    $('verification-progress').textContent = '已完成 ' + job.completed + '/' + job.total + ' · 合格 ' + rows.filter(r => r.qualified).length + next;
    body.replaceChildren();
    const labels = {pending:'等待验证',running:'正在验证',accepted:'已取得合格 state',network_failed:'代理连接失败',missing_state_header:'上游未返回状态头',invalid_state_envelope:'状态头格式无效',shape_mismatch:'收到有效封装，但不符合当前规则',state_time_rejected:'状态头时间不符合规则',incomplete_response:'上游回复未完整结束',upstream_rejected:'上游拒绝请求',cancelled:'已取消',skipped:'未验证'};
    for (const result of rows) {
      const row = document.createElement('tr'); row.dataset.verificationId = result.id;
      const name = cellText(result.name || result.id, [String(result.protocol || '').toUpperCase(), result.http_status ? 'HTTP ' + result.http_status : '', result.latency_ms ? result.latency_ms + ' ms' : ''].filter(Boolean).join(' · '));
      const attempted = !['pending','running','cancelled','skipped'].includes(result.status);
      const header = cellText(!attempted ? '尚无结果' : result.header_present ? (result.parsed ? '已收到 · 可解析' : '已收到 · 无法解析') : '未收到状态头', '实测 ' + (result.observed_length || 0) + ' 字符 / ' + (result.observed_blocks || 0) + ' 块；目标 ' + result.expected_length + ' 字符 / ' + result.expected_blocks + ' 块');
      const status = cellText(labels[result.status] || result.status, result.reason || '');
      status.dataset.state = result.qualified ? 'available' : result.status === 'shape_mismatch' ? 'restricted' : 'pending';
      row.append(name, header, status); body.append(row);
    }
    if (!rows.length) tableEmpty(body, '尚无逐节点结果');
  }
  clearTimeout(verificationTimer);
  if (jobs.some(j => activeVerificationStates.has(j.state))) verificationTimer = setTimeout(pollVerification, 10000);
}
function pollVerification() {
  if (!loaded || busy || document.hidden) { verificationTimer = setTimeout(pollVerification, 10000); return; }
  run(() => coreAction('status'));
}
function renderCore(report) {
  const valid = report?.schema === 1;
  const sessions = valid && Array.isArray(report.sessions) ? report.sessions : [];
  $('retry-route').disabled = !sessions.length;
  const pool = valid && Array.isArray(report.pool) ? report.pool : [];
  const body = $('sessions-body'); body.replaceChildren();
  const phases = { ready: '已准备', collecting: '正在采集', auth_blocked: '认证受限', access_paused: '访问冷却', rate_limited: '触发限流', passthrough: '普通转发', waiting_for_state: '等待 state', paused: '已暂停', cooldown: '冷却中' };
  const time = valid && report.generated_at ? new Date(report.generated_at) : null;
  $('core-snapshot').textContent = time && !Number.isNaN(time.getTime()) ? '快照时间：' + time.toLocaleString('zh-CN', { hour12: false }) + ' · 剩余时间按此快照计算' : '尚无快照';
  const engineCount = Array.isArray(report?.engines) ? report.engines.length : Number(report?.engines) || 0;
  $('core-status').textContent = report?.message || (valid ? engineCount + ' 个运行实例 · ' + sessions.length + ' 个会话 · 已处理 ' + (Number(report.requests_total) || 0) + ' 次请求' : '尚未读取运行状态。');
  $('core-status').dataset.error = String(!!report?.error_code);
  if (!sessions.length) tableEmpty(body, '暂无运行会话。启用插件并发送匹配的 OAuth 请求后再刷新。');
  for (const session of sessions) {
    const row = document.createElement('tr'); row.dataset.sessionId = session.id || session.session_id || '';
    const identity = cellText(session.model || '未知模型', '账号 ' + (session.account_id ?? '—'));
    const usable = session.usable === true;
    const state = cellText(usable ? 'state 可用' : 'state 不可用', '目标 ' + (session.expected_length ?? '—') + ' / 实测 ' + (session.observed_length ?? '—') + ' 字符');
    state.dataset.state = usable ? 'available' : 'pending';
    const expires = document.createElement('small'); expires.textContent = '剩余 ' + durationLabel(session.remaining_seconds) + ' · ' + (session.ready ? '已就绪' : '未就绪'); state.append(expires);
    const phase = phases[session.phase] || session.phase || '等待请求';
    const action = cellText(phase, session.diagnostic_message || '');
    if (session.cooldown_seconds > 0) { const cooldown = document.createElement('small'); cooldown.textContent = '冷却剩余 ' + durationLabel(session.cooldown_seconds); action.append(cooldown); }
    if (session.retry_after_seconds > 0) { const pause = document.createElement('small'); pause.textContent = '上游要求等待 ' + durationLabel(session.retry_after_seconds); action.append(pause); }
    const blocked = !row.dataset.sessionId || session.cooldown_seconds > 0 || [401, 403, 429].includes(session.rejected_status) || ['collecting', 'auth_blocked', 'rate_limited', 'paused', 'cooldown'].includes(session.phase);
    const retry = document.createElement('button'); retry.type = 'button'; retry.className = 'button button-secondary'; retry.textContent = '采集 state（消耗额度）';
    retry.dataset.sessionId = row.dataset.sessionId; retry.dataset.coreAction = 'retry'; retry.dataset.command = ''; retry.dataset.coreBlocked = String(blocked);
    retry.title = blocked ? '会话正在采集、已暂停或冷却中，请刷新状态后重试' : '使用此会话的账号凭据向上游采集 state，会消耗额度';
    action.append(retry); row.append(identity, state, action); body.append(row);
  }
  const poolBody = $('pool-body'); poolBody.replaceChildren();
  $('pool-status').textContent = pool.length ? pool.length + ' 个节点 · 状态更新于上方快照时间' : '暂无节点生命周期记录；启用生命周期池并产生请求后刷新。';
  if (!pool.length) tableEmpty(poolBody, '暂无节点生命周期记录');
  const poolStates = { available: '可用', in_use: '使用中', active: '使用中', exhausted: '已耗尽', disabled: '已停用', failed: '失败', cooling: '冷却中' };
  for (const node of pool) {
    const row = document.createElement('tr'); row.dataset.poolId = node.id;
    const name = cellText(node.name || node.id || '节点', String(node.protocol || '').toUpperCase() + ' · ' + node.id);
    const status = cellText(poolStates[node.state] || node.state || '未知', '采集 ' + (Number(node.attempts) || 0) + ' 次' + (node.reason ? ' · ' + node.reason : ''));
    const actions = document.createElement('td');
    for (const [action, label] of [['available', '回收'], ['disabled', '停用']]) {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'button button-secondary'; button.textContent = label;
      button.dataset.coreAction = 'pool'; button.dataset.poolAction = action; button.dataset.nodeId = node.id; button.dataset.command = ''; button.dataset.coreBlocked = String(node.state === action);
      actions.append(button);
    }
    row.append(name, status, actions); poolBody.append(row);
  }
  renderVerification(report, sessions);
  updateButtons(); resize();
}
function captureFields() {
  return new Map(Array.from(document.querySelectorAll('input, textarea, select'), el => [el.id, el.type === 'checkbox' ? el.checked : el.value]));
}
function renderResponse(config, submittedFields) {
  const changed = Array.from(captureFields()).filter(([id, value]) => submittedFields?.has(id) && submittedFields.get(id) !== value);
  render(config);
  for (const [id, value] of changed) { const el = $(id); if (el.type === 'checkbox') el.checked = value; else el.value = value; }
  updateButtons();
}
function render(config) {
  saved = config || {};
  for (const [id, key] of [['enabled','enabled'],['inject','inject_state'],['harvest','harvest_on_demand'],['closed','fail_closed'],['direct','direct'],['pool-enabled','pool_enabled']]) $(id).checked = !!saved[key];
  $('route-mode').value = saved.route_mode || 'round_robin';
  $('account-mode').value = saved.account_mode || (saved.state_target_length && saved.state_target_length !== 292 ? 'custom' : 'auto');
  $('state-refresh-mode').value = saved.state_refresh_mode || (saved.state_ttl_seconds ? 'standby' : 'on_demand');
  $('egress-mode').value = saved.egress_mode || 'state';
  $('proxy').value = (saved.proxy_urls || (saved.proxy_url ? [saved.proxy_url] : [])).join('\n');
  $('proxy-envs').value = (saved.proxy_envs || []).join('\n');
  $('subscriptions').value = displaySubscriptions(saved.subscriptions);
  for (const [id,key,fallback] of [['length','state_target_length',292],['ttl','state_ttl_seconds',3600],['refresh','refresh_before_seconds',600],['cooldown','cooldown_seconds',180],['subscription-refresh','subscription_refresh_seconds',900],['probe','probe_timeout_seconds',25],['probe-round','probe_round_seconds',20],['header-timeout','response_header_timeout_seconds',120],['body-limit','max_body_bytes',67108864],['max-probes','max_probes_per_round',6],['zstd-window','zstd_window_mib',64],['compact-limit','compact_limit_mib',64]]) $(id).value = saved[key] ?? fallback;
  $('models').value = (saved.models || ['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra']).join('\n');
  $('advanced-json').value = JSON.stringify(saved, null, 2);
  $('revision').textContent = '修订 ' + (saved.revision || 0);
  $('runtime-label').textContent = saved.enabled ? '策略开启' : '策略关闭';
  $('runtime-state').classList.toggle('is-on', !!saved.enabled);
  $('route-summary').textContent = '显式出口 ' + (saved.proxy_urls || (saved.proxy_url ? [saved.proxy_url] : [])).length + ' 个 · 订阅 ' + (saved.subscriptions || []).length + ' 个 · ' + (saved.route_mode === 'fixed' ? '固定节点' : '自动轮换');
  renderNodes(saved.route_report, saved.fixed_route_id);
  renderCore(saved.core_report);
  resize();
}
function validStateLength(length) {
  for (let n=1; n<=92; n++) if (Math.ceil((57+16*n)/3)*4 === length) return true;
  return false;
}
function readConfig() {
  const { route_request: ignoredRequest, core_request: ignoredCoreRequest, core_report: ignoredCoreReport, ...base } = saved;
  const config = { ...base };
  const roundBudget = Number($('probe-round').value);
  if (!Number.isInteger(roundBudget) || roundBudget < 1 || roundBudget > 60) throw new Error('首次采集总预算需在 1–60 秒范围内');
  config.probe_round_seconds = roundBudget;
  for (const [id,key] of [['enabled','enabled'],['inject','inject_state'],['harvest','harvest_on_demand'],['closed','fail_closed'],['direct','direct'],['pool-enabled','pool_enabled']]) config[key] = $(id).checked;
  for (const [id,key,min,max] of [['length','state_target_length',100,2040],['ttl','state_ttl_seconds',60,86400],['refresh','refresh_before_seconds',0,86399],['cooldown','cooldown_seconds',1,3600],['subscription-refresh','subscription_refresh_seconds',60,86400],['probe','probe_timeout_seconds',1,120],['header-timeout','response_header_timeout_seconds',1,120],['body-limit','max_body_bytes',1024,134217728],['max-probes','max_probes_per_round',1,20],['zstd-window','zstd_window_mib',16,128],['compact-limit','compact_limit_mib',16,128]]) {
    const value = Number($(id).value);
    if (!Number.isInteger(value) || value < min || value > max) throw new Error($(id).closest('label').querySelector('.field-label').textContent + '需在 ' + min + '–' + max + ' 范围内');
    config[key] = value;
  }
  if (!validStateLength(config.state_target_length)) throw new Error('state 长度不对应有效封装块数');
  if (config.refresh_before_seconds >= config.state_ttl_seconds) throw new Error('提前刷新需小于有效期');
  config.models = lines($('models').value);
  if (!config.models.length || config.models.length > 32 || new Set(config.models).size !== config.models.length) throw new Error('填写 1–32 个不重复的模型名称');
  config.proxy_urls = [...new Set(lines($('proxy').value).map(normalizeProxyUrl))];
  if (config.proxy_urls.length > 256) throw new Error('最多支持 256 个代理地址');
  delete config.proxy_url;
  config.proxy_envs = [...new Set(lines($('proxy-envs').value))];
  config.subscriptions = readSubscriptions($('subscriptions').value);
  config.route_mode = $('route-mode').value;
  config.fixed_route_id = $('fixed-route-id').value;
  if (config.route_mode === 'fixed' && !config.fixed_route_id) throw new Error('请先获取节点，再选择固定节点');
  config.account_mode = $('account-mode').value;
  config.state_refresh_mode = $('state-refresh-mode').value;
  config.egress_mode = $('egress-mode').value;
  config.egress_route = $('egress-route').value;
  if (config.egress_mode === 'fixed' && !config.egress_route) throw new Error('请先获取节点，再选择固定请求节点');
  return config;
}
async function saveConfig(config = readConfig()) {
  const fields = captureFields();
  delete config.core_report;
  const result = await send('config.save', { config });
  renderResponse(result.config, fields);
  setStatus('配置已保存');
  return result.config;
}
async function diagnose(operation, routeID) {
  const config = readConfig();
  const fields = captureFields();
  config.route_request = { operation, ...(routeID ? { route_id: routeID } : {}) };
  setStatus(operation === 'discover' ? '正在保存并获取节点…' : '正在测试节点连通性…');
  const result = await send('config.save', { config });
  renderResponse(result.config, fields);
  const report = result.config?.route_report;
  if (!report || report.schema !== 1) throw new Error('插件未返回节点列表，请确认已升级到本次插件包');
  setStatus($('nodes-status').textContent, !!report.error_code);
}
async function coreAction(operation, extra = {}) {
  const { route_request: ignoredRoute, core_request: ignoredCore, ...config } = saved;
  config.core_request = { operation, ...extra };
  setStatus(operation === 'retry' ? '正在提交 state 采集请求…' : operation === 'pool' ? '正在更新节点生命周期…' : '正在读取运行状态…');
  const result = await send('config.save', { config });
  saved = result.config || saved;
  $('revision').textContent = '修订 ' + (saved.revision || 0);
  renderCore(saved.core_report);
  if (!saved.core_report || saved.core_report.schema !== 1) throw new Error('插件未返回核心状态，请确认已升级到支持请求头保护的版本');
  setStatus(saved.core_report.message || (operation === 'retry' ? '采集请求已提交，请刷新运行状态查看结果' : operation === 'pool' ? '节点状态已更新' : '已刷新运行状态；表单中未保存的修改已保留'), !!saved.core_report.error_code);
}
async function run(action) {
  if (busy) return;
  busy = true; updateButtons();
  try { await action(); } catch (error) { setStatus(humanError(error), true); }
  finally { busy = false; updateButtons(); resize(); }
}
async function load() {
  setStatus('正在读取已保存配置…');
  try {
    const result = await send('config.load');
    loaded = true; render(result.config); setStatus('配置已加载');
  } catch (error) { loaded = false; updateButtons(); throw error; }
}
$('reload').addEventListener('click', () => run(load));
$('save').addEventListener('click', () => run(() => saveConfig()));
$('enable-protection').addEventListener('click', () => run(async () => {
  for (const id of ['enabled', 'inject', 'harvest']) $(id).checked = true;
  await saveConfig(); setStatus('请求头保护已开启；请确认宿主已启用插件并绑定 OAuth 路由');
}));
$('refresh-core').addEventListener('click', () => run(() => coreAction('status')));
$('refresh-pool').addEventListener('click', () => run(() => coreAction('status')));
$('refresh-verification').addEventListener('click', () => run(() => coreAction('status')));
$('allow-passthrough').addEventListener('click', () => run(async () => {
  $('closed').checked = false;
  await saveConfig(); setStatus('兼容转发已开启：没有合格 state 时正常转发，就绪后再注入；上游认证和限流约束仍生效。');
}));
$('verification-session').addEventListener('change', () => { renderVerification(saved.core_report, saved.core_report?.sessions || []); updateButtons(); });
$('retry-route').addEventListener('change', () => { renderVerification(saved.core_report, saved.core_report?.sessions || []); updateButtons(); });
$('verify-all-states').addEventListener('click', () => run(() => coreAction('verify_nodes', {session_id:$('verification-session').value})));
$('verify-one-state').addEventListener('click', () => run(() => coreAction('verify_nodes', {session_id:$('verification-session').value,route_ids:[$('retry-route').value]})));
$('cancel-verification').addEventListener('click', () => run(() => coreAction('cancel_verification', {session_id:$('verification-session').value})));
$('sessions-body').addEventListener('click', event => {
  const button = event.target.closest('button[data-core-action="retry"]');
  if (!button || button.disabled || busy || !loaded) return;
  const routeID = $('retry-route').value;
  run(() => coreAction('retry', { session_id: button.dataset.sessionId, ...(routeID ? { route_id: routeID } : {}) }));
});
$('pool-body').addEventListener('click', event => {
  const button = event.target.closest('button[data-core-action="pool"]');
  if (!button || button.disabled || busy || !loaded) return;
  run(() => coreAction('pool', { route_ids: [button.dataset.nodeId], action: button.dataset.poolAction }));
});
$('fetch-nodes').addEventListener('click', () => run(() => diagnose('discover')));
$('test-nodes').addEventListener('click', () => run(() => diagnose('test')));
$('test').addEventListener('click', () => run(() => diagnose('test')));
$('route-mode').addEventListener('change', updateButtons);
$('egress-mode').addEventListener('change', updateButtons);
$('account-mode').addEventListener('change', updateButtons);
$('nodes-body').addEventListener('click', event => {
  const button = event.target.closest('button[data-node-id]');
  if (!button || busy || !loaded) return;
  run(async () => {
    if (button.dataset.action === 'test') return diagnose('test', button.dataset.nodeId);
    $('route-mode').value = 'fixed'; $('fixed-route-id').value = button.dataset.nodeId;
    await saveConfig(); setStatus('固定节点已保存，后续请求使用此出口');
  });
});
$('save-json').addEventListener('click', () => run(() => saveConfig(JSON.parse($('advanced-json').value))));
document.querySelectorAll('[data-scroll]').forEach(button => button.addEventListener('click', () => {
  const section = $(button.dataset.scroll);
  if (section.tagName === 'DETAILS') section.open = true;
  section.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block:'start' });
  document.querySelectorAll('[data-scroll]').forEach(item => item.classList.toggle('is-active', item === button));
}));
const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(resize);
observer?.observe(document.body);
window.addEventListener('resize', resize, { passive: true });
render({});
if (token && parent !== window) {
  parent.postMessage({ source:'sub2api-plugin-ui', bridge_token:token, type:'sub2api.plugin.ready' }, '*');
  run(load);
} else setStatus('请从宿主插件配置页打开此界面', true);
