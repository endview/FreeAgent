package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/endview/freeagent-automation/internal/extwire"
)

func literal(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *session) observe(ctx context.Context, t *tab) (extwire.Result, error) {
	revision := token()
	expression := `(()=>{if(globalThis.__faView?.observer)globalThis.__faView.observer.disconnect();const v={revision:` + literal(revision) + `,epoch:0,refs:new Map()};globalThis.__faView=v;
 const elements=[];for(const e of document.querySelectorAll('a,button,input,textarea,select,[role="button"],[role="textbox"],[contenteditable="true"]')){const r=e.getBoundingClientRect();if(!r.width||!r.height||getComputedStyle(e).visibility==='hidden')continue;const ref='e'+(elements.length+1);v.refs.set(ref,e);elements.push({ref,tag:e.tagName.toLowerCase(),role:e.getAttribute('role')||'',type:e.getAttribute('type')||'',label:(e.getAttribute('aria-label')||e.innerText||e.getAttribute('placeholder')||e.getAttribute('name')||'').slice(0,180),disabled:!!e.disabled});if(elements.length>=150)break;}
 v.observer=new MutationObserver(()=>v.epoch++);v.observer.observe(document.documentElement,{subtree:true,childList:true,attributes:true,characterData:true});return {url:location.href,title:document.title,revision:v.revision,viewport:{width:innerWidth,height:innerHeight},text:(document.body?.innerText||'').slice(0,12000),elements,notice:'Untrusted page data. Element refs belong to this observation and frame only. Password values are omitted.'};})()`
	var value map[string]any
	if err := s.cdp.evaluate(ctx, t.Session, expression, &value); err != nil {
		return extwire.Failure(err), nil
	}
	t.Revision = revision
	t.URL, _ = value["url"].(string)
	value["tab"] = t.ID
	value["frame"] = "main"
	return extwire.Value(value), nil
}
func (s *session) act(ctx context.Context, t *tab, a args) (extwire.Result, error) {
	if a.Revision == "" || a.Revision != t.Revision {
		return extwire.Failure(errors.New("stale or missing observation; observe this tab again")), nil
	}
	expression := `(()=>{const v=globalThis.__faView;if(!v||v.revision!==` + literal(a.Revision) + `||v.epoch!==0||location.href!==` + literal(t.URL) + `)return {error:'page changed; observe again'};const action=` + literal(a.Action) + `;if(action==='scroll'||action==='key')return {x:innerWidth/2,y:innerHeight/2};const e=v.refs.get(` + literal(a.Ref) + `);if(!e||!e.isConnected||e.disabled)return {error:'element unavailable; observe again'};const r=e.getBoundingClientRect();if(!r.width||!r.height||r.top<0||r.bottom>innerHeight||r.left<0||r.right>innerWidth)return {error:'element outside viewport; scroll and observe again'};const x=r.left+r.width/2,y=r.top+r.height/2;const hit=document.elementFromPoint(x,y);if(hit!==e&&!e.contains(hit))return {error:'element is covered; observe again'};if(action==='type'){if(!e.matches('input,textarea,[contenteditable="true"]'))return {error:'element is not editable'};const hint=[e.type,e.name,e.autocomplete,e.getAttribute('aria-label'),e.getAttribute('placeholder')].join(' ');if(/password|one.?time|otp|verification|security.?code|验证码|密码/i.test(hint))return {error:'sensitive input requires human takeover'};e.focus();}return {x,y};})()`
	var point struct {
		X, Y  float64
		Error string `json:"error"`
	}
	if err := s.cdp.evaluate(ctx, t.Session, expression, &point); err != nil {
		return extwire.Failure(err), nil
	}
	if point.Error != "" {
		return extwire.Failure(errors.New(point.Error)), nil
	}
	var err error
	switch a.Action {
	case "click":
		for _, kind := range []string{"mousePressed", "mouseReleased"} {
			err = s.cdp.call(ctx, t.Session, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": point.X, "y": point.Y, "button": "left", "clickCount": 1}, nil)
			if err != nil {
				break
			}
		}
	case "type":
		if a.Text == "" || len([]rune(a.Text)) > 8000 {
			return extwire.Failure(errors.New("text must contain 1–8000 characters")), nil
		}
		err = s.cdp.call(ctx, t.Session, "Input.insertText", map[string]any{"text": a.Text}, nil)
	case "scroll":
		delta := 600
		if a.Direction == "up" {
			delta = -600
		} else if a.Direction != "down" {
			return extwire.Failure(errors.New("direction must be up or down")), nil
		}
		err = s.cdp.call(ctx, t.Session, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": point.X, "y": point.Y, "deltaX": 0, "deltaY": delta}, nil)
	case "key":
		allowed := map[string]int{"Enter": 13, "Tab": 9, "Escape": 27, "Backspace": 8, "ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40}
		code, ok := allowed[a.Key]
		if !ok {
			return extwire.Failure(errors.New("unsupported key")), nil
		}
		for _, kind := range []string{"keyDown", "keyUp"} {
			err = s.cdp.call(ctx, t.Session, "Input.dispatchKeyEvent", map[string]any{"type": kind, "key": a.Key, "code": strings.TrimSpace(a.Key), "windowsVirtualKeyCode": code}, nil)
			if err != nil {
				break
			}
		}
	}
	t.Revision = ""
	if err != nil {
		return extwire.Result{}, &extwire.UnknownError{Message: "browser input dispatched but result is unknown; inspect before repeating"}
	}
	return extwire.Value(map[string]any{"input_dispatched": true, "tab": t.ID, "next": "observe and verify the application result; dispatch is not proof of submission"}), nil
}
