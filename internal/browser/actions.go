package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Coordinates. Every x, y the actions in this file take (Scroll, Hover,
// Drag, ClickAt, ElementAt, Zoom) are CSS pixels of the viewport, with 0,0 at
// its top-left corner: the same frame as the PNGs Screenshot, Capture and
// Zoom return. Those are always viewport-sized at device scale 1 (one image
// pixel per CSS pixel), also on a display with a higher pixel ratio.

const (
	wheelTick    = 100 // CSS pixels one wheel tick scrolls
	defaultTicks = 3
	maxTicks     = 50
)

// viewportInfo is the visible area in CSS pixels.
type viewportInfo struct {
	w, h         float64
	pageX, pageY float64
	dpr          float64
}

func (b *Browser) viewport(ctx context.Context) (viewportInfo, error) {
	m, err := chromedp.Call(ctx, page.GetLayoutMetrics, cdp.Empty{})
	if err != nil {
		return viewportInfo{}, err
	}
	v := viewportInfo{dpr: 1}
	if vv := m.CSSVisualViewport; vv != nil {
		v.w, v.h, v.pageX, v.pageY = vv.ClientWidth, vv.ClientHeight, vv.PageX, vv.PageY
	}
	if v.w == 0 || v.h == 0 {
		if lv := m.CSSLayoutViewport; lv != nil {
			v.w, v.h = float64(lv.ClientWidth), float64(lv.ClientHeight)
		}
	}
	if r, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: "window.devicePixelRatio", ReturnByValue: new(true)}); err == nil && r.Result != nil {
		var d float64
		if json.Unmarshal(r.Result.Value, &d) == nil && d > 0 {
			v.dpr = d
		}
	}
	if v.w == 0 || v.h == 0 {
		return v, errors.New("the page has no visible area")
	}
	return v, nil
}

// objectOf resolves a ref's node to a remote object.
func (b *Browser) objectOf(ctx context.Context, ref string) (refInfo, runtime.RemoteObjectID, error) {
	info, err := b.lookup(ref)
	if err != nil {
		return info, "", err
	}
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: info.backend})
	if err != nil || obj.Object == nil {
		if err == nil {
			err = errors.New("not found")
		}
		return info, "", nodeGone(ref, err)
	}
	return info, obj.Object.ObjectID, nil
}

// quadCenter returns the centre of a node's first box, after scrolling it
// into view. It does not check what is on top of that point.
func (b *Browser) quadCenter(ctx context.Context, ref string, id cdp.BackendNodeID) (x, y float64, err error) {
	if _, err = chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: id}); err != nil {
		return 0, 0, nodeGone(ref, err)
	}
	q, err := chromedp.Call(ctx, dom.GetContentQuads, dom.GetContentQuadsParams{BackendNodeID: id})
	if err != nil {
		return 0, 0, nodeGone(ref, err)
	}
	for _, quad := range q.Quads {
		if len(quad) >= 8 {
			return (quad[0] + quad[2] + quad[4] + quad[6]) / 4, (quad[1] + quad[3] + quad[5] + quad[7]) / 4, nil
		}
	}
	return 0, 0, fmt.Errorf("%s is not visible on the page", ref)
}

func (b *Browser) finish(ctx context.Context, max time.Duration) (*Snapshot, error) {
	b.settle(ctx, max)
	return b.snapshot(ctx)
}

// pause waits d or until ctx ends.
func pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Scroll turns the mouse wheel amount ticks (default 3; one tick is about
// 100 px) in direction dir (up, down, left or right). The wheel is at the
// element ref names (so the scrollable container under it scrolls), else at
// x, y, else, when both are 0, at the centre of the viewport.
func (b *Browser) Scroll(call context.Context, ref string, x, y int, dir string, amount int) (*Snapshot, error) {
	var dx, dy float64
	if amount <= 0 {
		amount = defaultTicks
	}
	amount = min(amount, maxTicks)
	d := float64(amount * wheelTick)
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "down", "":
		dy = d
	case "up":
		dy = -d
	case "right":
		dx = d
	case "left":
		dx = -d
	default:
		return nil, fmt.Errorf("scroll direction must be up, down, left or right, not %q", dir)
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	px, py, err := b.pointFor(ctx, ref, x, y, false)
	if err != nil {
		return nil, err
	}
	if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseMoved, X: px, Y: py}); err != nil {
		return nil, err
	}
	if _, err := chromedp.Call(ctx, wheelCommand, wheelParams{Type: "mouseWheel", X: px, Y: py, DeltaX: dx, DeltaY: dy}); err != nil {
		return nil, err
	}
	pause(ctx, 250*time.Millisecond) // smooth scrolling animates
	return b.finish(ctx, 3*time.Second)
}

// wheelParams is a mouseWheel event. The protocol wants deltaX and deltaY
// both present, which input.DispatchMouseEventParams omits when zero.
type wheelParams struct {
	Type   string  `json:"type"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	DeltaX float64 `json:"deltaX"`
	DeltaY float64 `json:"deltaY"`
}

var wheelCommand = cdp.Command[wheelParams, cdp.Empty]{Method: input.CommandDispatchMouseEvent}

// pointFor is the point an action targets: the element's centre (hit-tested
// when hit is set), the given x, y, or the viewport's centre when both are 0.
func (b *Browser) pointFor(ctx context.Context, ref string, x, y int, hit bool) (float64, float64, error) {
	if ref != "" {
		info, err := b.lookup(ref)
		if err != nil {
			return 0, 0, err
		}
		if hit {
			return b.center(ctx, ref, info.backend)
		}
		return b.quadCenter(ctx, ref, info.backend)
	}
	v, err := b.viewport(ctx)
	if err != nil {
		return 0, 0, err
	}
	if x == 0 && y == 0 {
		return v.w / 2, v.h / 2, nil
	}
	if x < 0 || y < 0 || float64(x) >= v.w || float64(y) >= v.h {
		return 0, 0, fmt.Errorf("the point %d,%d is outside the viewport (%.0fx%.0f CSS pixels)", x, y, v.w, v.h)
	}
	return float64(x), float64(y), nil
}

// ScrollTo scrolls the element into view, also inside scrollable containers.
func (b *Browser) ScrollTo(call context.Context, ref string) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	info, err := b.lookup(ref)
	if err != nil {
		return nil, err
	}
	if _, err := chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: info.backend}); err != nil {
		return nil, nodeGone(ref, err)
	}
	pause(ctx, 100*time.Millisecond)
	return b.finish(ctx, 2*time.Second)
}

// Hover moves the mouse onto the element (or x, y) without clicking, for
// menus and tooltips, and gives the page a moment to react.
func (b *Browser) Hover(call context.Context, ref string, x, y int) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	px, py, err := b.pointFor(ctx, ref, x, y, true)
	if err != nil {
		return nil, err
	}
	// two moves: the first enters from the side, the second settles on the point
	for _, p := range [][2]float64{{math.Max(px-3, 0), py}, {px, py}} {
		if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseMoved, X: p[0], Y: p[1]}); err != nil {
			return nil, err
		}
	}
	pause(ctx, 200*time.Millisecond)
	return b.finish(ctx, 2*time.Second)
}

const setValueJS = `function(v){
  var el=this, tag=String(el.tagName||'').toLowerCase(), ty=(typeof el.type==='string'?el.type:'').toLowerCase();
  var role=el.getAttribute&&el.getAttribute('role');
  var desc='<'+tag+(tag==='input'&&ty?' type='+ty:'')+(role&&tag!=='input'?' role='+role:'')+'>';
  var fire=function(n){el.dispatchEvent(new Event(n,{bubbles:true}))};
  var native=function(P,val){Object.getOwnPropertyDescriptor(P,'value').set.call(el,val)};
  if(el.disabled)return {err:desc+' is disabled'};
  if(tag==='select'){
    var opts=Array.prototype.slice.call(el.options),m=null,i;
    for(i=0;i<opts.length;i++){if(opts[i].value===v){m=opts[i];break}}
    if(!m){var t=v.trim().toLowerCase();
      for(i=0;i<opts.length;i++){if(opts[i].text.trim()===v.trim()){m=opts[i];break}}
      if(!m)for(i=0;i<opts.length;i++){if(opts[i].text.trim().toLowerCase()===t){m=opts[i];break}}}
    if(!m)return {err:'no option with value or text '+JSON.stringify(v)+' in '+desc+'; options: '+opts.slice(0,20).map(function(o){return JSON.stringify(o.text.trim())}).join(', ')};
    if(m.disabled)return {err:'that option is disabled'};
    if(el.multiple){for(i=0;i<opts.length;i++)opts[i].selected=(opts[i]===m)}else{el.selectedIndex=m.index}
    fire('input');fire('change');return {ok:true};
  }
  if(tag==='input'){
    if(ty==='checkbox'||ty==='radio'){
      var s=v.trim().toLowerCase(),want;
      if(s==='true'||s==='on'||s==='1'||s==='yes'||s==='checked')want=true;
      else if(s==='false'||s==='off'||s==='0'||s==='no'||s==='unchecked')want=false;
      else return {err:desc+' takes true/false or on/off, not '+JSON.stringify(v)};
      if(ty==='radio'&&!want)return {err:'a radio button cannot be unchecked: select another one in its group'};
      if(el.checked!==want)el.click();
      if(el.checked!==want)return {err:'the page did not let '+desc+' change'};
      return {ok:true};
    }
    if(ty==='file'||ty==='button'||ty==='submit'||ty==='reset'||ty==='image'||ty==='hidden')return {err:desc+' has no value to set'};
    if(el.readOnly)return {err:desc+' is read-only'};
    native(HTMLInputElement.prototype,v);fire('input');fire('change');
    if(v!==''&&el.value==='')return {err:'the browser rejected '+JSON.stringify(v)+' for '+desc};
    return {ok:true};
  }
  if(tag==='textarea'){
    if(el.readOnly)return {err:desc+' is read-only'};
    native(HTMLTextAreaElement.prototype,v);fire('input');fire('change');return {ok:true};
  }
  if(el.isContentEditable){
    el.focus();var r=document.createRange();r.selectNodeContents(el);var sel=getSelection();sel.removeAllRanges();sel.addRange(r);
    var ok=v===''?document.execCommand('delete'):document.execCommand('insertText',false,v);
    if(!ok){el.textContent=v;fire('input')}
    return {ok:true};
  }
  return {err:desc+' is not a form field: use click or type'};
}`

// SetValue sets a form field like a user would (form_input): a <select> by
// option value or visible text, a checkbox or radio button ("true"/"false",
// "on"/"off"; a radio button can only be switched on), a range, number, date,
// colour, text input, textarea or contenteditable element. The value goes
// through the native setter followed by input and change events, so
// frameworks such as React see it. Password and payment fields are refused,
// as with Type.
func (b *Browser) SetValue(call context.Context, ref, value string) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	el, err := b.inspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	if el.Password {
		return nil, errors.New("werkz never types into password or payment fields")
	}
	info, obj, err := b.objectOf(ctx, ref)
	if err != nil {
		return nil, err
	}
	_, _ = chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: info.backend})
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: setValueJS, ObjectID: obj, ReturnByValue: new(true),
		Arguments: []*runtime.CallArgument{{Value: jsonv2.Value(mustJSON(value))}},
	})
	if err != nil {
		return nil, nodeGone(ref, err)
	}
	if res.ExceptionDetails != nil || res.Result == nil {
		return nil, fmt.Errorf("could not set the value of %s", ref)
	}
	var out struct {
		Err string `json:"err"`
	}
	if err := json.Unmarshal(res.Result.Value, &out); err != nil {
		return nil, err
	}
	if out.Err != "" {
		return nil, fmt.Errorf("%s: %s", ref, out.Err)
	}
	return b.finish(ctx, 3*time.Second)
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// Drag presses the mouse at the source (an element's centre, or fx, fy),
// moves to the target in about ten steps and releases it there. That moves
// sortable lists, sliders and canvases that listen to mouse events. When the
// source is an HTML5 draggable (draggable="true"), Chrome would start a native
// drag that synthetic mouse events cannot finish, so the drag is intercepted
// and replayed with Input.dispatchDragEvent (dragenter, dragover, drop)
// before the button is released.
func (b *Browser) Drag(call context.Context, fromRef string, fx, fy int, toRef string, tx, ty int) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	sx, sy, err := b.pointFor(ctx, fromRef, fx, fy, fromRef != "")
	if err != nil {
		return nil, err
	}
	ex, ey, err := b.pointFor(ctx, toRef, tx, ty, false)
	if err != nil {
		return nil, err
	}
	if fromRef == "" && fx == 0 && fy == 0 || toRef == "" && tx == 0 && ty == 0 {
		return nil, errors.New("a drag needs a source and a target (a ref or x, y for each)")
	}
	html5 := b.isDraggable(ctx, sx, sy)
	for len(b.drag) > 0 {
		<-b.drag
	}
	if html5 {
		if _, err := chromedp.Call(ctx, input.SetInterceptDrags, input.SetInterceptDragsParams{Enabled: true}); err != nil {
			html5 = false
		} else {
			defer func() {
				dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer cancel()
				_, _ = chromedp.Call(dctx, input.SetInterceptDrags, input.SetInterceptDragsParams{Enabled: false})
			}()
		}
	}
	mouse := func(t input.DispatchMouseEventType, x, y float64, buttons int64) error {
		p := input.DispatchMouseEventParams{Type: t, X: x, Y: y, Buttons: buttons}
		if t != input.DispatchMouseEventTypeMouseMoved || buttons != 0 {
			p.Button, p.ClickCount = input.MouseButtonLeft, 1
		}
		_, err := chromedp.Call(ctx, input.DispatchMouseEvent, p)
		return err
	}
	if err := mouse(input.DispatchMouseEventTypeMouseMoved, sx, sy, 0); err != nil {
		return nil, err
	}
	if err := mouse(input.DispatchMouseEventTypeMousePressed, sx, sy, 1); err != nil {
		return nil, err
	}
	const steps = 10
	var data *input.DragData
	for i := 1; i <= steps; i++ {
		x, y := sx+(ex-sx)*float64(i)/steps, sy+(ey-sy)*float64(i)/steps
		if err := mouse(input.DispatchMouseEventTypeMouseMoved, x, y, 1); err != nil {
			return nil, err
		}
		if html5 && data == nil {
			select {
			case d := <-b.drag:
				data = d.data
			case <-time.After(40 * time.Millisecond):
			}
		}
		if data != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if data != nil {
		for _, t := range []input.DispatchDragEventType{input.DispatchDragEventTypeDragEnter, input.DispatchDragEventTypeDragOver, input.DispatchDragEventTypeDrop} {
			if _, err := chromedp.Call(ctx, input.DispatchDragEvent, input.DispatchDragEventParams{Type: t, X: ex, Y: ey, Data: data}); err != nil {
				return nil, err
			}
		}
	}
	if err := mouse(input.DispatchMouseEventTypeMouseMoved, ex, ey, 1); err != nil {
		return nil, err
	}
	if err := mouse(input.DispatchMouseEventTypeMouseReleased, ex, ey, 0); err != nil {
		return nil, err
	}
	return b.finish(ctx, 3*time.Second)
}

type dragData struct{ data *input.DragData }

func (b *Browser) noteDrag(e *input.EventDragIntercepted) {
	b.mu.Lock()
	ch := b.drag
	b.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- dragData{e.Data}:
	default:
	}
}

// isDraggable reports whether the element at the point is (inside) an HTML5
// draggable.
func (b *Browser) isDraggable(ctx context.Context, x, y float64) bool {
	expr := fmt.Sprintf(`(function(){var e=document.elementFromPoint(%g,%g);return !!(e&&e.closest&&e.closest('[draggable="true"]'))})()`, x, y)
	r, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: expr, ReturnByValue: new(true)})
	return err == nil && r.Result != nil && string(r.Result.Value) == "true"
}

var modBits = map[string]int64{
	"alt": 1, "option": 1, "opt": 1, "ctrl": 2, "control": 2,
	"meta": 4, "cmd": 4, "command": 4, "super": 4, "win": 4, "windows": 4, "shift": 8,
}

// parseModifiers turns "ctrl+shift" into CDP's modifier bit field.
func parseModifiers(s string) (int64, error) {
	var m int64
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '+' || r == ',' || r == ' ' }) {
		bit, ok := modBits[strings.ToLower(p)]
		if !ok {
			return 0, fmt.Errorf("unknown modifier %q (use ctrl, alt, shift, cmd/meta)", p)
		}
		m |= bit
	}
	return m, nil
}

// ClickAt clicks at x, y (viewport CSS pixels, the frame of Screenshot) with
// button left, right or middle (default left), count clicks (1 to 3) and the
// given modifiers ("ctrl+shift", ...). A form submission it causes is blocked
// unless allow.Submit, as with Click. Use ElementAt first to see what is
// there.
func (b *Browser) ClickAt(call context.Context, x, y int, button string, count int, modifiers string, allow Allow) (*Snapshot, error) {
	var btn input.MouseButton
	var buttons int64
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "left", "":
		btn, buttons = input.MouseButtonLeft, 1
	case "right":
		btn, buttons = input.MouseButtonRight, 2
	case "middle":
		btn, buttons = input.MouseButtonMiddle, 4
	default:
		return nil, fmt.Errorf("button must be left, right or middle, not %q", button)
	}
	if count == 0 {
		count = 1
	}
	if count < 1 || count > 3 {
		return nil, fmt.Errorf("click count must be 1, 2 or 3, not %d", count)
	}
	mods, err := parseModifiers(modifiers)
	if err != nil {
		return nil, err
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	if x == 0 && y == 0 {
		return nil, errors.New("click point 0,0 is not a valid target")
	}
	px, py, err := b.pointFor(ctx, "", x, y, false)
	if err != nil {
		return nil, err
	}
	if _, err := b.elementAt(ctx, x, y); err != nil {
		return nil, err
	}
	end, err := b.startAllow(ctx, allow)
	if err != nil {
		return nil, err
	}
	defer end()
	if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseMoved, X: px, Y: py, Modifiers: mods}); err != nil {
		return nil, err
	}
	for i := 1; i <= count; i++ {
		for _, ev := range []input.DispatchMouseEventParams{
			{Type: input.DispatchMouseEventTypeMousePressed, X: px, Y: py, Button: btn, Buttons: buttons, ClickCount: int64(i), Modifiers: mods},
			{Type: input.DispatchMouseEventTypeMouseReleased, X: px, Y: py, Button: btn, ClickCount: int64(i), Modifiers: mods},
		} {
			if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, ev); err != nil {
				return nil, err
			}
		}
	}
	b.settle(ctx, 5*time.Second)
	end()
	return b.snapshot(ctx)
}

const closestInteractiveJS = `function(){
  var el=this.nodeType===1?this:this.parentElement;
  return (el&&el.closest&&el.closest('a[href],button,input,select,textarea,summary,label,[role=button],[role=link],[role=checkbox],[role=radio],[role=menuitem],[role=tab],[role=switch],[role=option],[contenteditable=""],[contenteditable=true],[tabindex]'))||el;
}`

// ElementAt reports what is at x, y (viewport CSS pixels): the element, or
// the link, button or field it is part of. Ref is set when that element has
// one in the current page tree (ReadPage, Find or a snapshot). Nothing on the
// page is touched.
func (b *Browser) ElementAt(call context.Context, x, y int) (*Element, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	return b.elementAt(ctx, x, y)
}

func (b *Browser) elementAt(ctx context.Context, x, y int) (*Element, error) {
	if _, _, err := b.pointFor(ctx, "", x, y, false); err != nil {
		return nil, err
	}
	hit, err := chromedp.Call(ctx, dom.GetNodeForLocation, dom.GetNodeForLocationParams{X: int64(x), Y: int64(y)})
	if err != nil || hit.BackendNodeID == 0 {
		return nil, fmt.Errorf("nothing at %d,%d", x, y)
	}
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: hit.BackendNodeID})
	if err != nil || obj.Object == nil {
		return nil, fmt.Errorf("nothing at %d,%d", x, y)
	}
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{FunctionDeclaration: closestInteractiveJS, ObjectID: obj.Object.ObjectID})
	if err != nil || res.Result == nil || res.Result.ObjectID == "" {
		return nil, fmt.Errorf("nothing at %d,%d", x, y)
	}
	return b.describeObject(ctx, res.Result.ObjectID, "")
}

// Wait pauses for d (at most 30s), then returns the page as it is.
func (b *Browser) Wait(call context.Context, d time.Duration) (*Snapshot, error) {
	d = max(0, min(d, 30*time.Second))
	ctx, done, err := b.beginFor(call, d+opTimeout)
	if err != nil {
		return nil, err
	}
	defer done()
	pause(ctx, d)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.finish(ctx, 5*time.Second)
}

// viewportOverride is the viewport override Resize set.
type viewportOverride struct {
	w, h   int64
	mobile bool
	touch  bool
}

// applyEmulation puts the current viewport override on the tab ctx belongs to.
func (b *Browser) applyEmulation(ctx context.Context) error {
	b.mu.Lock()
	e := b.emul
	b.mu.Unlock()
	if e == nil {
		return nil
	}
	return setEmulation(ctx, e)
}

func setEmulation(ctx context.Context, e *viewportOverride) error {
	if e == nil {
		if _, err := chromedp.Call(ctx, emulation.ClearDeviceMetricsOverride, cdp.Empty{}); err != nil {
			return err
		}
		_, err := chromedp.Call(ctx, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: false})
		return err
	}
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width: e.w, Height: e.h, DeviceScaleFactor: 1, Mobile: e.mobile,
	}); err != nil {
		return err
	}
	_, err := chromedp.Call(ctx, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: e.touch})
	return err
}

// Resize changes the viewport of every werkz tab: preset "mobile" (390x844,
// mobile emulation with touch), "tablet" (820x1180, touch, desktop layout
// rules), "desktop" (no override: the browser window's own size, 1280x800 for a
// browser werkz launched) or "" / "custom" with w and h in pixels. The device
// scale factor stays 1, so screenshots keep one pixel per CSS pixel.
func (b *Browser) Resize(call context.Context, preset string, w, h int) error {
	var e *viewportOverride
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "mobile":
		e = &viewportOverride{w: 390, h: 844, mobile: true, touch: true}
	case "tablet":
		e = &viewportOverride{w: 820, h: 1180, touch: true}
	case "desktop":
		e = nil
	case "", "custom":
		if w < 100 || w > 4096 || h < 100 || h > 4096 {
			return fmt.Errorf("a custom size must be between 100 and 4096 pixels each way, not %dx%d", w, h)
		}
		e = &viewportOverride{w: int64(w), h: int64(h)}
	default:
		return fmt.Errorf("unknown preset %q (use mobile, tablet, desktop, or a width and height)", preset)
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return err
	}
	defer done()
	if err := setEmulation(ctx, e); err != nil {
		return fmt.Errorf("could not resize: %w", err)
	}
	b.mu.Lock()
	b.emul = e
	b.mu.Unlock()
	pause(ctx, 150*time.Millisecond)
	return nil
}
