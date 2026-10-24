"""Check real plugin resources in same-origin and desktop-origin sandbox frames.

Requires TestPluginUIBrowserFixture and Python Playwright. No user credentials.
"""
import argparse
import json
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--fixture',required=True)
parser.add_argument('--output',required=True)
parser.add_argument('--expect-blocked',action='store_true')
parser.add_argument('--legacy-headers',action='store_true')
parser.add_argument('--keep-alive',action='store_true')
args=parser.parse_args()
fixture=json.loads(Path(args.fixture).read_text())
base=fixture['backend_url']
assert urlparse(base).hostname=='127.0.0.1'
out=Path(args.output);out.mkdir(parents=True,exist_ok=True)
results=[]
try:
 with sync_playwright() as p:
  browser=p.chromium.launch(headless=True)
  for origin in [base,'http://tauri.localhost','http://untrusted.localhost']:
   page=browser.new_page(viewport={'width':1100,'height':950})
   # A synthetic browser-origin fixture needs explicit local-network access;
   # this does not disable CSP, framing, origin or sandbox enforcement.
   page.context.grant_permissions(['local-network-access'])
   if args.legacy_headers:
    def legacy(route):
     response=route.fetch()
     headers=dict(response.headers)
     headers['x-frame-options']='SAMEORIGIN'
     headers['content-security-policy']=headers['content-security-policy'].replace("frame-ancestors 'self' tauri://localhost http://tauri.localhost https://tauri.localhost","frame-ancestors 'self'")
     route.fulfill(response=response,headers=headers)
    page.route('**/api/v1/plugin-ui/**',legacy)
   errors=[]
   failures=[]
   requests=[]
   page.on('console',lambda message:errors.append(message.text) if message.type=='error' else None)
   page.on('request',lambda r:requests.append({'origin':urlparse(r.url).netloc,'kind':r.resource_type}))
   page.on('requestfailed',lambda r:failures.append({'origin':urlparse(r.url).netloc,'reason':r.failure}))
   def parent(route):
    route.fulfill(status=200,content_type='text/html',body='''<html><body><iframe sandbox="allow-scripts" style="width:100%;height:850px"></iframe><script>
window.ready=false;addEventListener('message',e=>{if(e.data?.type==='sub2api.plugin.ready')window.ready=true;
if(e.data?.type==='config.load')e.source.postMessage({source:'sub2api-plugin-host',bridge_token:e.data.bridge_token,request_id:e.data.request_id,ok:true,config:{}},'*');});
</script></body></html>''')
   page.route(origin+'/parent',parent)
   session=page.request.post(base+'/api/v1/admin/plugins/1/ui-session').json()['data']
   page.goto(origin+'/parent')
   page.locator('iframe').evaluate('(el,url)=>el.src=url',base+session['url'])
   ready=False
   try:page.wait_for_function('window.ready===true',timeout=4000);ready=True
   except Exception:pass
   results.append({'origin':origin if origin!=base else 'same-origin','ready':ready,'console_errors':errors,'requests':requests,'failures':failures,'frame_urls':[urlparse(f.url).scheme+'://'+urlparse(f.url).netloc for f in page.frames]})
   if ready:
    assert page.frame_locator('iframe').get_by_text('默认保护策略',exact=True).is_visible()
   page.screenshot(path=str(out/('embedding-'+str(len(results))+'.png')),full_page=True)
   page.close()
  browser.close()
 (out/'result.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf-8')
 assert results[0]['ready'],results
 assert results[1]['ready'] != args.expect_blocked,results
 assert not results[2]['ready'],results
 print(json.dumps(results,ensure_ascii=False))
finally:
 if not args.keep_alive:Path(fixture['done_file']).write_text('done')
