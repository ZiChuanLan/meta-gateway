import json,time,base64
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
from urllib.parse import urlparse
MODELS=['gpt-4o','claude-sonnet-4-6','deepseek-v4-flash','gemini-2.5-pro','gpt-image-1','audit-long-model-name-with-a-very-long-version-and-custom-mapping-2026']
# A long catalogue, so the layout work can be judged against the case that broke
# it: a site that publishes 100+ models in one price table.
MODELS+=[f'audit-bulk-{i:03d}' for i in range(1,121)]
PNG='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jD9sAAAAASUVORK5CYII='
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def send(self,obj,code=200):
  raw=json.dumps(obj,ensure_ascii=False).encode();self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  p=urlparse(self.path).path
  # Ordered before the real list: `/empty/v1/models` also ends with `/v1/models`.
  if p.endswith('/empty/v1/models'):return self.send({'object':'list','data':[]})
  if p.endswith('/v1/models'):return self.send({'object':'list','data':[{'id':m,'object':'model','owned_by':'audit'} for m in MODELS]})
  # The status shape too, or the empty table reads as a parse failure instead
  # of an empty catalogue. Ordered first: `/empty/api/status` also ends with
  # `/api/status`.
  if p.endswith('/empty/api/status'):return self.send({'success':True,'data':{'quota_per_unit':500000,'quota_display_type':'CNY','usd_exchange_rate':7.3,'custom_currency_symbol':'¥'}})
  if p.endswith('/api/status'):return self.send({'success':True,'data':{'quota_per_unit':500000,'quota_display_type':'CNY','usd_exchange_rate':7.3,'custom_currency_symbol':'¥'}})
  # Three source shapes for the "collected nothing / cannot collect" walkthrough:
  # `/empty/...` publishes a price table with no rows (a successful, empty
  # catalogue), `/invalid/...` publishes a list the parser must reject, and
  # `:4399` has nothing listening at all.
  if p.endswith('/invalid/api/pricing'):return self.send({'success':True,'group_ratio':{'default':1},'data':[]})
  if p.endswith('/empty/api/pricing'):return self.send({'success':True,'group_ratio':{'default':1}})
  if p.endswith('/api/pricing'):return self.send({'success':True,'group_ratio':{'default':1,'vip':0.5},'data':[{'model_name':m,'quota_type':0,'model_ratio':1.25,'completion_ratio':4,'cache_ratio':0.1,'enable_groups':['default','vip']} for m in MODELS]})
  if p.endswith('/api/user/self'):return self.send({'success':True,'data':{'id':42,'username':'audit','quota':12500000,'used_quota':300000,'group':'default'}})
  if p.endswith('/api/user/self/groups'):return self.send({'success':True,'data':['default','vip']})
  if p.endswith('/api/token'):return self.send({'success':True,'data':[{'id':1,'name':'local-mock-key','key':'mock-upstream-only','group':'default','status':1}]})
  if p.endswith('/api/user/checkin'):return self.send({'success':True,'data':{'checked_in':False}})
  if p=='/empty':return self.send({'rows':[]})
  if p=='/healthz':return self.send({'status':'ok'})
  return self.send({'success':True,'data':[]})
 def do_POST(self):
  p=urlparse(self.path).path;raw=self.rfile.read(int(self.headers.get('Content-Length','0')))
  try: body=json.loads(raw or b'{}')
  except: body={}
  if p.endswith('/api/user/checkin'):return self.send({'success':True,'data':{'reward':10}})
  if '/fail/' in p:return self.send({'error':{'message':'Audit mock failure','type':'upstream_error'}},503)
  if p.endswith('/images/generations'):return self.send({'created':int(time.time()),'data':[{'b64_json':PNG}],'usage':{'input_tokens':100,'output_tokens':100,'total_tokens':200}})
  if p.endswith('/chat/completions'):
   model=body.get('model','gpt-4o');usage={'prompt_tokens':1200,'completion_tokens':120,'total_tokens':1320,'prompt_tokens_details':{'cached_tokens':200}}
   if body.get('stream'):
    self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Cache-Control','no-cache');self.end_headers()
    for text in ['这是隔离的本地测试。','用于审查布局、流式输出和取消操作。','没有连接真实上游，也没有产生实际费用。']:
     chunk={'id':'audit-stream','object':'chat.completion.chunk','model':model,'choices':[{'index':0,'delta':{'content':text}}]}
     try:self.wfile.write(('data: '+json.dumps(chunk,ensure_ascii=False)+'\n\n').encode());self.wfile.flush();time.sleep(3 if any('cancel-slow' in str(m.get('content','')) for m in body.get('messages',[])) else .35)
     except (BrokenPipeError,ConnectionResetError):return
    self.wfile.write(('data: '+json.dumps({'choices':[],'usage':usage})+'\n\ndata: [DONE]\n\n').encode());return
   return self.send({'id':'audit-chat','object':'chat.completion','model':model,'choices':[{'index':0,'message':{'role':'assistant','content':'本地模拟响应：用于 UI 审查，不是生产数据。'},'finish_reason':'stop'}],'usage':usage})
  return self.send({'success':True,'data':{}})
# 0.0.0.0 so the gateway container can reach it over the compose network.
ThreadingHTTPServer(('0.0.0.0',4319),Handler).serve_forever()
