"""Render the real PluginsView against synthetic admin data and actual plugin assets.

Uses the compiled desktop frontend in dist/ and intercepts its backend API.
No installed user's host is contacted.
TestPluginUIBrowserFixture serves the unchanged signed package resources.
"""
import argparse
import json
import mimetypes
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright,expect

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--fixture',required=True)
parser.add_argument('--output',required=True)
args=parser.parse_args()
fixture=json.loads(Path(args.fixture).read_text())
backend=fixture['backend_url']
assert urlparse(backend).hostname=='127.0.0.1'
out=Path(args.output);out.mkdir(parents=True,exist_ok=True)
frontend=Path(__file__).resolve().parent.parent
admin={'id':987,'username':'UI 验证','email':'ui@example.invalid','role':'admin','balance':100,'concurrency':5,'status':'active'}
with sync_playwright() as p:
 browser=p.chromium.launch(headless=True)
 context=browser.new_context(viewport={'width':1600,'height':1000},permissions=['local-network-access'])
 context.add_init_script('''if(window===window.top){localStorage.setItem('auth_token','synthetic-ui-fixture');localStorage.setItem('auth_user',%s);localStorage.setItem('sub2api_locale','zh');localStorage.setItem('admin_guide_987_admin_v4_interactive','true');}'''%json.dumps(json.dumps(admin)))
 page=context.new_page();page.set_default_timeout(45000)
 installed=page.request.get(backend+'/fixture/plugin').json()
 plugins=[]
 for i in range(12):
  value={**installed,'id':i+1,'state':'disabled','bindings':[],'runtime_healthy':False,'runtime_message':''}
  if i:
   value.update(name=['请求策略','用量观察','格式检查','模型参数','会话检查','访问规则'][i%6]+f' {i+1}',plugin_key=f'example.extension-{i+1}',description='独立扩展 · 可按需配置和启用。')
  plugins.append(value)
 saved={};writes=[];errors=[];broken=False
 page.on('pageerror',lambda e:errors.append(str(e)))
 def route_all(route):
  req=route.request;u=urlparse(req.url);path=u.path
  if u.hostname=='tauri.localhost':
   if path.startswith('/api/'):
    route.fulfill(status=404,body='wrong desktop resource origin');return
   target=frontend/'dist'/path.lstrip('/')
   if target.is_file():route.fulfill(status=200,content_type=mimetypes.guess_type(str(target))[0] or 'application/octet-stream',body=target.read_bytes())
   else:route.fulfill(status=200,content_type='text/html',body=(frontend/'dist/index.html').read_text(encoding='utf-8').replace('<head>','<head><base href="/">'))
   return
  if u.netloc in ('127.0.0.1:19995','127.0.0.1:19765'):
   cors={'Access-Control-Allow-Origin':'http://tauri.localhost','Access-Control-Allow-Credentials':'true','Access-Control-Allow-Methods':'GET, POST, PUT, DELETE, OPTIONS','Access-Control-Allow-Headers':req.headers.get('access-control-request-headers','Authorization, Content-Type, X-Timezone, X-Request-ID')}
   if req.method=='OPTIONS':route.fulfill(status=204,headers=cors);return
   if path.startswith('/api/v1/plugin-ui/'):
    if broken:route.fulfill(status=410,body='expired fixture');return
    response=route.fetch(url=backend+path);route.fulfill(response=response);return
   if path.endswith('/ui-session'):
    response=page.request.post(backend+'/api/v1/admin/plugins/1/ui-session')
    route.fulfill(status=response.status,content_type='application/json',headers=cors,body=response.body());return
   value={}
   if path.endswith('/auth/me'):value=admin
   elif path.endswith('/admin/plugins'):value=plugins
   elif path.endswith('/config') and '/plugins/' in path:
    if req.method=='PUT':
     saved.clear();saved.update(req.post_data_json);writes.append('config.save')
    value=saved
   elif path.endswith('/test') and '/plugins/' in path:value={'success':True,'message':'fixture','latency_ms':1}
   elif '/settings' in path:value={'site_name':'Sub2API','plugin_management_enabled':True,'custom_menu_items':[],'ops_monitoring_enabled':True,'backend_mode_enabled':False}
   elif path.endswith('/compliance'):value={'required':False,'version':'fixture'}
   elif 'announcements' in path:value=[]
   elif '/setup/status' in path:value={'needs_setup':False}
   elif '/version' in path:value={'version':'0.2.5','build_type':'plugin-preview'}
   elif '/plugins/' in path:value=plugins[0]
   route.fulfill(status=200,content_type='application/json',headers=cors,body=json.dumps({'code':0,'data':value}));return
  route.abort()
 context.route('**/*',route_all)
 try:
  page.goto('http://tauri.localhost/admin/plugins',wait_until='domcontentloaded')
  rows=page.locator('article[data-plugin-id]');expect(rows).to_have_count(12)
  welcome=page.get_by_role('dialog').filter(has_text='欢迎')
  if welcome.count():welcome.get_by_role('button').filter(has_text='关闭').first.click()
  assert rows.first.bounding_box()['height']<160
  page.screenshot(path=str(out/'plugins-desktop.png'),full_page=True)
  rows.first.get_by_role('button',name='详情与管理',exact=True).click()
  expect(page.locator('#plugin-details-1')).to_be_visible()
  expect(page.locator('#plugin-details-2')).not_to_be_visible()
  rows.first.get_by_role('button',name='收起详情',exact=True).click()
  page.get_by_role('searchbox').fill('example.extension-12');expect(rows).to_have_count(1)
  page.get_by_role('searchbox').fill('');expect(rows).to_have_count(12)
  rows.first.get_by_role('button',name='配置',exact=True).click()
  frame=page.frame_locator('iframe')
  expect(frame.get_by_text('默认保护策略',exact=True)).to_be_visible()
  expect(frame.locator('#status')).to_contain_text('配置就绪')
  frame.locator('#mode').select_option('mode1');frame.get_by_role('button',name='保存配置',exact=True).click()
  expect(frame.locator('#status')).to_have_text('配置已保存。')
  assert writes==['config.save']
  page.screenshot(path=str(out/'configuration-desktop.png'),full_page=True)
  page.get_by_role('button',name='Close modal',exact=True).click()
  page.set_viewport_size({'width':390,'height':844})
  page.screenshot(path=str(out/'plugins-mobile.png'),full_page=True)
  assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
  page.set_viewport_size({'width':1600,'height':1000});broken=True
  rows.first.get_by_role('button',name='配置',exact=True).click()
  expect(page.get_by_role('alert')).to_contain_text('配置页面未响应',timeout=20000)
  page.screenshot(path=str(out/'configuration-load-error.png'),full_page=True)
  broken=False;page.get_by_role('button',name='重新加载配置页',exact=True).click()
  expect(page.frame_locator('iframe').get_by_text('默认保护策略',exact=True)).to_be_visible()
  expect(page.frame_locator('iframe').locator('#status')).to_contain_text('配置就绪')
  assert not errors,errors
  result={'passed':True,'unchanged_plugin':True,'desktop_origin':'http://tauri.localhost','synthetic_plugin_count':12,'checks':['compact_rows','search','independent_details','real_plugin_configuration','bridge_save','mobile_no_overflow','load_timeout','retry'],'page_errors':errors}
  (out/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8');print(json.dumps(result,ensure_ascii=False))
 except Exception:
  page.screenshot(path=str(out/'failure.png'),full_page=True)
  (out/'failure.txt').write_text(page.locator('body').inner_text(),encoding='utf-8')
  print('Page errors:',errors)
  raise
 finally:context.unroute_all(behavior='ignoreErrors');context.close();browser.close();Path(fixture['done_file']).write_text('done')
