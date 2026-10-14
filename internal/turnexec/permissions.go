package turnexec

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/mitkox/esf/internal/sandbox"
	"net/http"
	"time"
)

// MCP stdio gate. Input is data, never shell text. Only the exact pending
// operation can be answered, and each answer is consumed by a unique ID/digest.
const permissionScript = `import sys,json,os,time,uuid,hashlib
schema={'type':'object','properties':{'tool_name':{'type':'string'},'input':{'type':'object'},'tool_use_id':{'type':'string'}},'required':['tool_name','input']}
def gate(args):
 req={'id':str(uuid.uuid4()),'tool_name':args['tool_name'],'input':args['input']}
 raw=json.dumps(req,sort_keys=True,separators=(',',':')).encode()
 if len(raw)>16384: return {'behavior':'deny','message':'operation exceeds approval size limit'}
 digest=hashlib.sha256(raw).hexdigest()
 with open('/tmp/esf-permission.new','wb') as f: f.write(raw)
 os.replace('/tmp/esf-permission.new','/tmp/esf-permission.json')
 deadline=time.monotonic()+1800
 while time.monotonic()<deadline:
  try:
   answer=json.load(open('/tmp/esf-decision.json'))
   if answer['id']==req['id'] and answer['operation_digest']==digest:
    if answer['decision']=='allow': return {'behavior':'allow','updatedInput':args['input']}
    return {'behavior':'deny','message':'sf human decision denied this operation'}
  except (OSError,ValueError,KeyError): pass
  time.sleep(0.25)
 return {'behavior':'deny','message':'sf approval timed out'}
for line in sys.stdin:
 try:
  msg=json.loads(line)
  if 'id' not in msg: continue
  method=msg.get('method')
  if method=='initialize': result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'sf-gate','version':'1'}}
  elif method=='tools/list': result={'tools':[{'name':'approve','description':'Request sf human decision for an exact operation','inputSchema':schema}]}
  elif method=='tools/call' and msg.get('params',{}).get('name')=='approve': result={'content':[{'type':'text','text':json.dumps(gate(msg['params']['arguments']))}]}
  elif method=='ping': result={}
  else:
   print(json.dumps({'jsonrpc':'2.0','id':msg['id'],'error':{'code':-32601,'message':'Unknown method'}}),flush=True);continue
  print(json.dumps({'jsonrpc':'2.0','id':msg['id'],'result':result}),flush=True)
 except Exception:
  if 'msg' in locals() and 'id' in msg: print(json.dumps({'jsonrpc':'2.0','id':msg['id'],'error':{'code':-32603,'message':'Permission bridge failed closed'}}),flush=True)
`

func (s *Service) pollPermission(ctx context.Context, sb sandbox.Sandbox, r *record) error {
	data, err := sb.ReadFile(ctx, "/tmp/esf-permission.json")
	if err != nil {
		return nil
	}
	if len(data) > 16384 {
		return errors.New("permission operation exceeds limit")
	}
	var operation struct {
		ID       string          `json:"id"`
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if json.Unmarshal(data, &operation) != nil || !idPattern.MatchString(operation.ID) || operation.ToolName == "" || len(operation.Input) == 0 {
		return errors.New("invalid permission request")
	}
	operationDigest := digest(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := r.Decisions[operation.ID]; ok {
		if previous.OperationDigest != operationDigest {
			return errors.New("permission identity conflict")
		}
		b, _ := json.Marshal(previous)
		return sb.WriteFile(ctx, "/tmp/esf-decision.json", b)
	}
	if r.PendingPermission != nil && r.PendingPermission.ID == operation.ID {
		if r.PendingPermission.OperationDigest != operationDigest {
			return errors.New("permission operation changed")
		}
		return nil
	}
	if r.PendingPermission != nil && r.PendingPermission.Decision == "" {
		return errors.New("concurrent permission request")
	}
	if len(r.Decisions) >= 1000 {
		return errors.New("permission count limit exceeded")
	}
	permission := Permission{ID: operation.ID, OperationDigest: operationDigest, Description: operation.ToolName + ": " + string(operation.Input)}
	r.PendingPermission = &permission
	r.Status.Permissions = []Permission{permission}
	s.event(r, "permission", permission)
	return s.persist(r)
}
func (s *Service) answerPermission(w http.ResponseWriter, r *http.Request, rec *record) {
	var answer struct {
		OwnerToken string `json:"owner_token"`
		ID         string `json:"permission_id"`
		Digest     string `json:"operation_digest"`
		Decision   string `json:"decision"`
	}
	if decode(w, r, &answer, 4096) != nil || (answer.Decision != "allow" && answer.Decision != "deny") {
		respond(w, 400, map[string]string{"error": "invalid permission decision"})
		return
	}
	ownerHash := digest([]byte(answer.OwnerToken))
	if subtle.ConstantTimeCompare([]byte(ownerHash), []byte(rec.OwnerHash)) != 1 {
		respond(w, 403, map[string]string{"error": "ownership mismatch"})
		return
	}
	if old, ok := rec.Decisions[answer.ID]; ok {
		if old.OperationDigest == answer.Digest && old.Decision == answer.Decision {
			respond(w, 200, rec.Status)
		} else {
			respond(w, 409, map[string]string{"error": "permission decision conflict"})
		}
		return
	}
	permission := rec.PendingPermission
	if terminal(rec.Status.State) || !time.Now().Before(rec.Status.LeaseExpiresAt) || permission == nil || permission.ID != answer.ID || permission.OperationDigest != answer.Digest {
		respond(w, 409, map[string]string{"error": "stale permission decision"})
		return
	}
	permission.Decision = answer.Decision
	if rec.Decisions == nil {
		rec.Decisions = map[string]Permission{}
	}
	rec.Decisions[permission.ID] = *permission
	rec.Status.Permissions = nil
	s.event(rec, "permission_decision", map[string]string{"id": permission.ID, "operation_digest": permission.OperationDigest, "decision": permission.Decision})
	if s.persist(rec) != nil {
		rec.cancel()
		respond(w, 500, map[string]string{"error": "decision persistence failed"})
		return
	}
	respond(w, 200, rec.Status)
}
