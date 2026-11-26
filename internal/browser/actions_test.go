package browser

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// loadPage serves body as a page and opens it.
func loadPage(t *testing.T, b *Browser, body string) (*Snapshot, string) {
	t.Helper()
	s := serve(t, html(body))
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return snap, s.URL
}

func ev(t *testing.T, b *Browser, js string) string {
	t.Helper()
	out, err := b.Eval(ctx5(t), js)
	if err != nil {
		t.Fatalf("eval %q: %v", js, err)
	}
	return out
}

func evNum(t *testing.T, b *Browser, js string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.Split(ev(t, b, js), ".")[0])
	if err != nil {
		t.Fatalf("eval %q is not a number: %v", js, err)
	}
	return n
}

// refNamed finds the ref of a line with that name, whatever its role.
func refNamed(t *testing.T, tree, name string) string {
	t.Helper()
	m := regexp.MustCompile(`\[(ref_\d+)\] \S+ "` + regexp.QuoteMeta(name) + `"`).FindStringSubmatch(tree)
	if m == nil {
		t.Fatalf("no ref named %q in tree:\n%s", name, tree)
	}
	return m[1]
}

// center returns the integer centre of an element's box in viewport CSS pixels.
func centerOf(t *testing.T, b *Browser, id string) (int, int) {
	t.Helper()
	js := `(function(){var r=document.getElementById('` + id + `').getBoundingClientRect();return Math.round(r.left+r.width/2)+','+Math.round(r.top+r.height/2)})()`
	p := strings.Split(ev(t, b, js), ",")
	x, _ := strconv.Atoi(p[0])
	y, _ := strconv.Atoi(p[1])
	return x, y
}

func TestScrollAndScrollTo(t *testing.T) {
	b := br(t)
	snap, _ := loadPage(t, b, `<div id=box style="height:100px;width:200px;overflow:auto"><button id=top>Top</button>
<div style="height:1000px"></div><button id=deep>Deep</button></div>
<div id=hbox style="width:100px;overflow:auto;white-space:nowrap"><button>Left</button><span style="display:inline-block;width:1000px"></span></div>
<div style="height:3000px">filler</div>`)
	if _, err := b.Scroll(ctx5(t), refNamed(t, snap.Tree, "Top"), 0, 0, "down", 2); err != nil {
		t.Fatal(err)
	}
	if n := evNum(t, b, `document.getElementById('box').scrollTop`); n < 150 || n > 250 {
		t.Errorf("scroll at a ref: box.scrollTop = %d, want about 200", n)
	}
	if n := evNum(t, b, `window.scrollY`); n != 0 {
		t.Errorf("the page scrolled too: %d", n)
	}
	// at x, y, and upwards
	if _, err := b.Scroll(ctx5(t), "", 50, 50, "up", 1); err != nil {
		t.Fatal(err)
	}
	if n := evNum(t, b, `document.getElementById('box').scrollTop`); n < 50 || n > 150 {
		t.Errorf("scroll up at x,y: scrollTop = %d, want about 100", n)
	}
	// the page centre, default amount
	if _, err := b.Scroll(ctx5(t), "", 0, 0, "down", 0); err != nil {
		t.Fatal(err)
	}
	if n := evNum(t, b, `window.scrollY`); n < 250 || n > 350 {
		t.Errorf("scroll at the page centre: scrollY = %d, want about 300", n)
	}
	// sideways inside a container
	snap, _ = b.Navigate(ctx5(t), b.URL())
	if _, err := b.Scroll(ctx5(t), refNamed(t, snap.Tree, "Left"), 0, 0, "right", 3); err != nil {
		t.Fatal(err)
	}
	if n := evNum(t, b, `document.getElementById('hbox').scrollLeft`); n < 250 {
		t.Errorf("scroll right: scrollLeft = %d", n)
	}
	if _, err := b.Scroll(ctx5(t), "", 0, 0, "sideways", 1); err == nil {
		t.Error("a bad direction was accepted")
	}
	if _, err := b.Scroll(ctx5(t), "", 5000, 5000, "down", 1); err == nil || !strings.Contains(err.Error(), "outside the viewport") {
		t.Errorf("point outside the viewport: %v", err)
	}
	// ScrollTo reaches an element inside a scroll container
	snap, _ = b.Navigate(ctx5(t), b.URL())
	if n := evNum(t, b, `document.getElementById('box').scrollTop`); n != 0 {
		t.Fatalf("fresh page already scrolled: %d", n)
	}
	if _, err := b.ScrollTo(ctx5(t), refNamed(t, snap.Tree, "Deep")); err != nil {
		t.Fatal(err)
	}
	if n := evNum(t, b, `document.getElementById('box').scrollTop`); n < 800 {
		t.Errorf("ScrollTo: box.scrollTop = %d", n)
	}
	if got := ev(t, b, `(function(){var b=document.getElementById('box').getBoundingClientRect(),d=document.getElementById('deep').getBoundingClientRect();return d.top>=b.top&&d.bottom<=b.bottom+1})()`); got != "true" {
		t.Error("the element is not inside the container's view after ScrollTo")
	}
}

func TestHover(t *testing.T) {
	b := br(t)
	snap, _ := loadPage(t, b, `<style>.sub{display:none}.m:hover .sub{display:block}</style>
<div class=m><button>Menu</button><div class=sub><button>Sub item</button></div></div>
<div id=tip style="position:absolute;left:300px;top:300px;width:50px;height:50px" onmouseover="this.dataset.over='yes'">tip</div>`)
	if strings.Contains(snap.Tree, "Sub item") {
		t.Fatal("the menu is open before hovering")
	}
	snap, err := b.Hover(ctx5(t), refNamed(t, snap.Tree, "Menu"), 0, 0)
	if err != nil || !strings.Contains(snap.Tree, "Sub item") {
		t.Fatalf("hover did not reveal the menu: %v\n%s", err, snap.Tree)
	}
	if _, err := b.Hover(ctx5(t), "", 320, 320); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `document.getElementById('tip').dataset.over`); got != "yes" {
		t.Errorf("hover at x,y: mouseover not seen (%q)", got)
	}
}

func TestSetValue(t *testing.T) {
	b := br(t)
	snap, _ := loadPage(t, b, `<select id=s aria-label=Fruit><option value=a>Apple</option><option value=b>Banana</option></select>
<input type=checkbox id=c aria-label=Agree><input type=radio name=r id=r1 aria-label=R1><input type=radio name=r id=r2 aria-label=R2>
<input type=range id=rg min=0 max=10 aria-label=Vol><input type=number id=n aria-label=Num><input type=date id=d aria-label=When>
<input type=color id=col aria-label=Hue><input type=text id=t aria-label=Name><textarea id=ta aria-label=Notes></textarea>
<div id=ce contenteditable role=textbox aria-label=Editor></div><input type=password id=pw aria-label=Pw><button id=bt>Go</button>
<script>window.ev=[];window.instSet=0;
['s','c','r1','rg','n','d','col','t','ta'].forEach(function(i){var e=document.getElementById(i);
 ['input','change'].forEach(function(n){e.addEventListener(n,function(){window.ev.push(i+':'+n)})})});
var t=document.getElementById('t');var nd=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value');
Object.defineProperty(t,'value',{get:function(){return nd.get.call(this)},set:function(v){window.instSet++;nd.set.call(this,v)},configurable:true});
</script>`)
	set := func(name, v string) {
		t.Helper()
		if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, name), v); err != nil {
			t.Fatalf("SetValue %s=%q: %v", name, v, err)
		}
	}
	set("Fruit", "Banana") // by visible text
	if got := ev(t, b, `document.getElementById('s').value`); got != "b" {
		t.Errorf("select by text: %q", got)
	}
	set("Fruit", "a") // by value
	if got := ev(t, b, `document.getElementById('s').value`); got != "a" {
		t.Errorf("select by value: %q", got)
	}
	if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, "Fruit"), "Cherry"); err == nil || !strings.Contains(err.Error(), "no option") {
		t.Errorf("unknown option: %v", err)
	}
	if got := ev(t, b, `window.ev.filter(function(e){return e.indexOf('s:')==0}).join()`); got != "s:input,s:change,s:input,s:change" {
		t.Errorf("select events: %q", got)
	}
	set("Agree", "true")
	if ev(t, b, `document.getElementById('c').checked`) != "true" {
		t.Error("checkbox not checked")
	}
	set("Agree", "off")
	if ev(t, b, `document.getElementById('c').checked`) != "false" {
		t.Error("checkbox not unchecked")
	}
	set("R2", "on")
	if ev(t, b, `document.getElementById('r2').checked`) != "true" {
		t.Error("radio not selected")
	}
	if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, "R2"), "false"); err == nil {
		t.Error("a radio button was unchecked")
	}
	set("Vol", "7")
	set("Num", "42")
	set("When", "2024-05-06")
	set("Hue", "#ff0000")
	set("Name", "Ada")
	set("Notes", "line1\nline2")
	set("Editor", "typed in an editor")
	for id, want := range map[string]string{"rg": "7", "n": "42", "d": "2024-05-06", "col": "#ff0000", "t": "Ada", "ta": "line1\nline2"} {
		if got := ev(t, b, `document.getElementById('`+id+`').value`); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if got := ev(t, b, `document.getElementById('ce').textContent`); got != "typed in an editor" {
		t.Errorf("contenteditable = %q", got)
	}
	if got := ev(t, b, `window.ev.filter(function(e){return e=='t:input'||e=='t:change'}).join()`); got != "t:input,t:change" {
		t.Errorf("text events: %q", got)
	}
	if got := ev(t, b, `window.instSet`); got != "0" {
		t.Errorf("the value went through the instance setter (%s sets): frameworks would not see it", got)
	}
	if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, "Pw"), "hunter2"); err == nil || !strings.Contains(err.Error(), "never types into password") {
		t.Errorf("password field: %v", err)
	}
	if _, err := b.SetValue(ctx5(t), refNamed(t, snap.Tree, "Go"), "x"); err == nil || !strings.Contains(err.Error(), "<button>") {
		t.Errorf("button: %v", err)
	}
}

func TestKeys(t *testing.T) {
	b := br(t)
	s, hits := countingServer(t, `<input id=a aria-label=A value="hello"><input id=b aria-label=B><input type=password id=pw aria-label=Pw>
<form method=post action=/post><input id=f name=q aria-label=Q><button>Send</button></form>
<button onclick="document.getElementById('out').textContent='len:'+document.getElementById('pw').value.length">Check</button><div id=out></div>
<script>window.k=[];document.addEventListener('keydown',function(e){window.k.push((e.ctrlKey?'c+':'')+(e.shiftKey?'s+':'')+(e.metaKey?'m+':'')+e.key+'/'+e.code)})</script>`)
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	click := func(name string) {
		t.Helper()
		if _, err := b.Click(ctx5(t), refNamed(t, snap.Tree, name), Allow{}); err != nil {
			t.Fatal(err)
		}
	}
	active := func() string { return ev(t, b, `document.activeElement.id`) }
	value := func(id string) string { return ev(t, b, `document.getElementById('`+id+`').value`) }
	pwLen := func() string { // a secret field's value is out of Eval's reach: ask the page
		click("Check")
		return ev(t, b, `document.getElementById('out').textContent`)
	}

	click("A")
	if _, err := b.Key(ctx5(t), "Tab", 1, Allow{}); err != nil || active() != "b" {
		t.Fatalf("Tab: %v, focus on %q", err, active())
	}
	if _, err := b.Key(ctx5(t), "shift+Tab", 1, Allow{}); err != nil || active() != "a" {
		t.Fatalf("shift+Tab: %v, focus on %q", err, active())
	}
	if _, err := b.Key(ctx5(t), "End Backspace Backspace", 1, Allow{}); err != nil || value("a") != "hel" {
		t.Fatalf("Backspace: %v, value %q", err, value("a"))
	}
	if _, err := b.Key(ctx5(t), "Backspace", 2, Allow{}); err != nil || value("a") != "h" {
		t.Fatalf("repeat: %v, value %q", err, value("a"))
	}
	if _, err := b.Key(ctx5(t), "ctrl+a Backspace", 1, Allow{}); err != nil || value("a") != "" {
		t.Fatalf("ctrl+a: %v, value %q", err, value("a"))
	}
	if _, err := b.Key(ctx5(t), "h i", 1, Allow{}); err != nil || value("a") != "hi" {
		t.Fatalf("characters: %v, value %q", err, value("a"))
	}
	if _, err := b.Key(ctx5(t), "cmd+a Backspace", 1, Allow{}); err != nil || value("a") != "" {
		t.Fatalf("cmd+a: %v, value %q", err, value("a"))
	}
	ev(t, b, `window.k=[]`)
	if _, err := b.Key(ctx5(t), "cmd+shift+k ArrowDown Escape", 1, Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.k.join()`); !strings.Contains(got, "s+m+K/KeyK") || !strings.Contains(got, "ArrowDown/ArrowDown") || !strings.Contains(got, "Escape/Escape") {
		t.Errorf("key events seen by the page: %s", got)
	}
	for _, bad := range []string{"", "Hyper", "foo+a", "ctrl+"} {
		if _, err := b.Key(ctx5(t), bad, 1, Allow{}); err == nil {
			t.Errorf("Key(%q) was accepted", bad)
		}
	}

	// the focused element, and secret fields
	if el, err := b.FocusedElement(ctx5(t)); err != nil || el.Tag != "input" || el.Password || el.InForm {
		t.Errorf("FocusedElement on A: %+v, %v", el, err)
	}
	click("Pw")
	if el, err := b.FocusedElement(ctx5(t)); err != nil || !el.Password || el.Type != "password" {
		t.Errorf("FocusedElement on the password field: %+v, %v", el, err)
	}
	for _, k := range []string{"a", "shift+a", "Space", "ctrl+v", "cmd+v", "ctrl+c", "shift+Insert", "x y"} {
		if _, err := b.Key(ctx5(t), k, 1, Allow{}); err == nil || !strings.Contains(err.Error(), "never types into password or payment fields") {
			t.Errorf("Key(%q) in a password field: %v", k, err)
		}
	}
	if pwLen() != "len:0" {
		t.Error("something reached the password field")
	}
	if _, err := b.Key(ctx5(t), "Tab", 1, Allow{}); err != nil {
		t.Errorf("Tab out of a password field: %v", err)
	}
	// Tab can land in a secret field in the middle of a sequence
	click("A")
	if _, err := b.Key(ctx5(t), "Tab Tab z", 1, Allow{}); err == nil || pwLen() != "len:0" {
		t.Errorf("typing after Tab into the password field: %v (%s)", err, pwLen())
	}

	// Enter in a form field submits: blocked unless allowed
	click("Q")
	if el, err := b.FocusedElement(ctx5(t)); err != nil || !el.InForm {
		t.Errorf("FocusedElement in the form: %+v, %v", el, err)
	}
	if _, err := b.Key(ctx5(t), "x", 1, Allow{}); err != nil {
		t.Fatal(err)
	}
	got, err := b.Key(ctx5(t), "Enter", 1, Allow{})
	if err != nil || !hasNote(got.Blocked, "form submit") || hits.n("POST /post") != 0 {
		t.Fatalf("Enter without Allow.Submit: %v %+v (POSTs %d)", err, got, hits.n("POST /post"))
	}
	click("Send")
	if el, err := b.FocusedElement(ctx5(t)); err != nil || !el.Submits || el.Tag != "button" {
		t.Errorf("FocusedElement on the submit button: %+v, %v", el, err)
	}
	if got, err = b.Key(ctx5(t), "Enter", 1, Allow{}); err != nil || !hasNote(got.Blocked, "form submit") || hits.n("POST /post") != 0 {
		t.Fatalf("Enter on a submit button: %v %+v", err, got)
	}
	got, err = b.Key(ctx5(t), "Enter", 1, Allow{Submit: true})
	if err != nil || hits.n("POST /post") != 1 || !strings.Contains(got.Tree, "posted") {
		t.Fatalf("Enter with Allow.Submit: %v %+v (POSTs %d)", err, got, hits.n("POST /post"))
	}
}

func TestTypeText(t *testing.T) {
	b := br(t)
	s, hits := countingServer(t, `<input id=a aria-label=A><input type=password id=pw aria-label=Pw><input name=card_number aria-label=Card id=card>
<form method=post action=/post><input id=f name=q aria-label=Q></form><textarea id=ta aria-label=Notes></textarea>
<button onclick="document.getElementById('out').textContent='len:'+document.getElementById('pw').value.length+','+document.getElementById('card').value.length">Check</button><div id=out></div>`)
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.TypeText(ctx5(t), "hello", Allow{}); err == nil || !strings.Contains(err.Error(), "nothing has focus: click a field first") {
		t.Errorf("no focus: %v", err)
	}
	at := func(id string) {
		t.Helper()
		x, y := centerOf(t, b, id)
		if _, err := b.ClickAt(ctx5(t), x, y, "left", 1, "", Allow{}); err != nil {
			t.Fatal(err)
		}
	}
	at("a")
	if _, err := b.TypeText(ctx5(t), "typed text", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `document.getElementById('a').value`); got != "typed text" {
		t.Errorf("value = %q", got)
	}
	for _, id := range []string{"pw", "card"} {
		at(id)
		if _, err := b.TypeText(ctx5(t), "secret", Allow{}); err == nil || !strings.Contains(err.Error(), "werkz never types into password or payment fields") {
			t.Errorf("%s: %v", id, err)
		}
	}
	chk := func() {
		x, y := centerOf(t, b, "out")
		_ = x
		snap, _ := b.ReadPage(ctx5(t), true)
		if _, err := b.Click(ctx5(t), refNamed(t, snap, "Check"), Allow{}); err != nil {
			t.Fatal(err)
		}
		if got := ev(t, b, `document.getElementById('out').textContent`); got != "len:0,0" {
			t.Errorf("a secret field received text: %s (y=%d)", got, y)
		}
	}
	chk()
	// newlines are Enter presses: a textarea gets them, a form field submits
	at("ta")
	if _, err := b.TypeText(ctx5(t), "one\ntwo", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `document.getElementById('ta').value`); got != "one\ntwo" {
		t.Errorf("textarea = %q", got)
	}
	at("f")
	got, err := b.TypeText(ctx5(t), "query\n", Allow{})
	if err != nil || !hasNote(got.Blocked, "form submit") || hits.n("POST /post") != 0 {
		t.Fatalf("newline in a form without Allow.Submit: %v %+v", err, got)
	}
	if v := ev(t, b, `document.getElementById('f').value`); v != "query" {
		t.Errorf("form field = %q", v)
	}
	if _, err := b.TypeText(ctx5(t), "\n", Allow{Submit: true}); err != nil || hits.n("POST /post") != 1 {
		t.Errorf("newline with Allow.Submit: %v (POSTs %d)", err, hits.n("POST /post"))
	}
	_ = snap
}

func TestClickAtAndElementAt(t *testing.T) {
	b := br(t)
	s, hits := countingServer(t, `<div id=box style="position:absolute;left:100px;top:100px;width:200px;height:100px;background:#ddd">box</div>
<form method=post action=/post style="position:absolute;left:400px;top:300px"><button id=sb>Pay</button></form>
<a id=lk href="/there" style="position:absolute;left:100px;top:400px">Link</a>
<script>window.log=[];var bx=document.getElementById('box');
['mousedown','mouseup','click','dblclick','contextmenu','auxclick'].forEach(function(n){bx.addEventListener(n,function(e){
 window.log.push(n+':'+e.button+(e.ctrlKey?':ctrl':'')+(e.shiftKey?':shift':'')+(n==='click'?':d'+e.detail:''));if(n==='contextmenu')e.preventDefault()})});</script>`)
	snap, err := b.Navigate(ctx5(t), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	bx, by := centerOf(t, b, "box")
	if _, err := b.ClickAt(ctx5(t), bx, by, "right", 1, "", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.log.join()`); !strings.Contains(got, "mousedown:2") || !strings.Contains(got, "contextmenu:2") {
		t.Errorf("right click: %s", got)
	}
	ev(t, b, `window.log=[]`)
	if _, err := b.ClickAt(ctx5(t), bx, by, "left", 2, "", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.log.join()`); !strings.Contains(got, "dblclick:0") || !strings.Contains(got, "click:0:d2") {
		t.Errorf("double click: %s", got)
	}
	ev(t, b, `window.log=[]`)
	if _, err := b.ClickAt(ctx5(t), bx, by, "left", 1, "ctrl+shift", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.log.join()`); !strings.Contains(got, "click:0:ctrl:shift") {
		t.Errorf("modifiers: %s", got)
	}
	ev(t, b, `window.log=[]`)
	if _, err := b.ClickAt(ctx5(t), bx, by, "middle", 1, "", Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.log.join()`); !strings.Contains(got, "mousedown:1") {
		t.Errorf("middle click: %s", got)
	}
	if _, err := b.ClickAt(ctx5(t), bx, by, "back", 1, "", Allow{}); err == nil {
		t.Error("a bad button was accepted")
	}
	if _, err := b.ClickAt(ctx5(t), bx, by, "left", 4, "", Allow{}); err == nil {
		t.Error("a bad count was accepted")
	}
	if _, err := b.ClickAt(ctx5(t), bx, by, "left", 1, "hyper", Allow{}); err == nil {
		t.Error("a bad modifier was accepted")
	}

	// what is there, and the submit guard
	sx, sy := centerOf(t, b, "sb")
	el, err := b.ElementAt(ctx5(t), sx, sy)
	if err != nil || !el.Submits || el.Tag != "button" || !el.InForm || el.Ref != refNamed(t, snap.Tree, "Pay") || el.Name != "Pay" {
		t.Fatalf("ElementAt on the submit button: %+v, %v", el, err)
	}
	lx, ly := centerOf(t, b, "lk")
	if el, err := b.ElementAt(ctx5(t), lx, ly); err != nil || el.Href != s.URL+"/there" || el.Submits {
		t.Errorf("ElementAt on a link: %+v, %v", el, err)
	}
	if el, err := b.ElementAt(ctx5(t), bx, by); err != nil || el.Submits || el.Tag != "div" || el.Ref != "" {
		t.Errorf("ElementAt on a plain div: %+v, %v", el, err)
	}
	if _, err := b.ElementAt(ctx5(t), 99999, 5); err == nil {
		t.Error("ElementAt outside the viewport was accepted")
	}
	got, err := b.ClickAt(ctx5(t), sx, sy, "left", 1, "", Allow{})
	if err != nil || !hasNote(got.Blocked, "form submit") || hits.n("POST /post") != 0 {
		t.Fatalf("ClickAt on a submit button: %v %+v", err, got)
	}
	got, err = b.ClickAt(ctx5(t), sx, sy, "left", 1, "", Allow{Submit: true})
	if err != nil || hits.n("POST /post") != 1 || !strings.Contains(got.Tree, "posted") {
		t.Fatalf("ClickAt with Allow.Submit: %v %+v", err, got)
	}
}

func TestDrag(t *testing.T) {
	b := br(t)
	loadPage(t, b, `<div id=h style="position:absolute;left:50px;top:50px;width:40px;height:40px;background:red"></div>
<div id=t style="position:absolute;left:300px;top:200px;width:100px;height:100px;background:blue"></div>
<div id=src draggable=true style="position:absolute;left:50px;top:300px;width:80px;height:40px;background:green">drag me</div>
<div id=dst style="position:absolute;left:400px;top:400px;width:120px;height:80px;background:yellow">drop here</div>
<script>var d=false;window.h5=[];
var h=document.getElementById('h');h.onmousedown=function(){d=true};
document.onmousemove=function(e){if(d){h.style.left=(e.clientX-20)+'px';h.style.top=(e.clientY-20)+'px'}};
document.onmouseup=function(e){if(d){d=false;window.dropped=e.clientX+','+e.clientY}};
var s=document.getElementById('src'),t=document.getElementById('dst');
s.addEventListener('dragstart',function(e){e.dataTransfer.setData('text/plain','payload');window.h5.push('dragstart')});
t.addEventListener('dragover',function(e){e.preventDefault()});
t.addEventListener('drop',function(e){e.preventDefault();window.h5.push('drop:'+e.dataTransfer.getData('text/plain'))});
</script>`)
	if _, err := b.Drag(ctx5(t), "", 70, 70, "", 350, 250); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.dropped`); got != "350,250" {
		t.Errorf("mouse drag released at %q", got)
	}
	if got := ev(t, b, `document.getElementById('h').style.left`); got != "330px" {
		t.Errorf("the dragged element is at %q", got)
	}
	sx, sy := centerOf(t, b, "src")
	dx, dy := centerOf(t, b, "dst")
	if _, err := b.Drag(ctx5(t), "", sx, sy, "", dx, dy); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `window.h5.join()`); got != "dragstart,drop:payload" {
		t.Errorf("HTML5 drag and drop: %q", got)
	}
	if _, err := b.Drag(ctx5(t), "", 0, 0, "", 5, 5); err == nil {
		t.Error("a drag without a source was accepted")
	}
}

func pngSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestZoomAndScreenshotScale(t *testing.T) {
	b := br(t)
	loadPage(t, b, `<h1>Hello</h1><p>some text to look at</p>`)
	z, err := b.Zoom(ctx5(t), 10, 10, 110, 60)
	if err != nil || z.Width != 100 || z.Height != 50 {
		t.Fatalf("Zoom: %+v, %v", z, err)
	}
	if _, err := os.Stat(z.Path); err != nil {
		t.Error(err)
	}
	vw, vh := evNum(t, b, `window.innerWidth`), evNum(t, b, `window.innerHeight`)
	if z, err := b.Zoom(ctx5(t), vw-80, vh-50, 5000, 5000); err != nil || z.Width != 80 || z.Height != 50 {
		t.Errorf("Zoom clipped to the viewport: %+v, %v", z, err)
	}
	if _, err := b.Zoom(ctx5(t), 5000, 5000, 6000, 6000); err == nil {
		t.Error("an empty region was accepted")
	}
	if s, err := b.Screenshot(ctx5(t), false, false); err != nil || s.Width != vw || s.Height != vh {
		t.Errorf("Screenshot: %+v, %v (viewport %dx%d)", s, err, vw, vh)
	}
	// a high-density display: still one image pixel per CSS pixel
	ctx := ctx5(t)
	b.opMu.Lock()
	_, err = chromedp.Call(b.tab, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(vw), Height: int64(vh), DeviceScaleFactor: 2})
	b.opMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Resize(ctx, "desktop", 0, 0) }()
	if s, err := b.Screenshot(ctx, false, false); err != nil || s.Width != vw || s.Height != vh {
		t.Errorf("Screenshot at pixel ratio 2: %+v, %v (viewport %dx%d)", s, err, vw, vh)
	}
	if data, err := b.Capture(ctx); err != nil {
		t.Error(err)
	} else if w, h := pngSize(t, data); w != vw || h != vh {
		t.Errorf("Capture at pixel ratio 2: %dx%d", w, h)
	}
	if z, err := b.Zoom(ctx, 0, 0, 100, 50); err != nil || z.Width != 100 || z.Height != 50 {
		t.Errorf("Zoom at pixel ratio 2: %+v, %v", z, err)
	}
}

func TestFindAndPageText(t *testing.T) {
	b := br(t)
	var many strings.Builder
	for i := 0; i < 30; i++ {
		many.WriteString(`<button>Item ` + strconv.Itoa(i) + `</button>`)
	}
	snap, _ := loadPage(t, b, `<nav><a href="/x">NavLink</a></nav><main><h1>Title</h1><p>Body   text
here</p><button>Save</button><button>Cancel</button>
<input aria-label="Secret card" name=card_number value="4111111111111111"><input type=password aria-label=Pw value=hunter22>
<textarea name=card_number aria-label=area>4222222222222222</textarea>`+many.String()+`</main><footer>FooterText</footer>`)
	read, _ := b.ReadPage(ctx5(t), false)
	save := refNamed(t, read, "Save")
	got, err := b.Find(ctx5(t), "save", 0)
	if err != nil || len(got) != 1 || got[0] != `[`+save+`] button "Save"` {
		t.Errorf("Find by text: %v, %v (want %s)", got, err, save)
	}
	if got, _ := b.Find(ctx5(t), "SAVE", 5); len(got) != 1 {
		t.Errorf("Find is case-sensitive: %v", got)
	}
	got, _ = b.Find(ctx5(t), "link", 0)
	if len(got) == 0 || !strings.Contains(got[0], `link "NavLink"`) {
		t.Errorf("Find by role: %v", got)
	}
	if got, _ := b.Find(ctx5(t), "button cancel", 0); len(got) != 1 || !strings.Contains(got[0], "Cancel") {
		t.Errorf("Find role + text: %v", got)
	}
	if got, _ := b.Find(ctx5(t), "heading", 0); len(got) != 1 || !strings.Contains(got[0], `heading "Title"`) {
		t.Errorf("Find heading: %v", got)
	}
	if got, _ := b.Find(ctx5(t), "button", 0); len(got) != 20 {
		t.Errorf("Find button returned %d lines, want the cap of 20", len(got))
	}
	if got, _ := b.Find(ctx5(t), "button", 3); len(got) != 3 {
		t.Errorf("Find limit: %d", len(got))
	}
	for _, secret := range []string{"4111", "hunter22", "4222"} {
		if got, _ := b.Find(ctx5(t), secret, 0); len(got) != 0 {
			t.Errorf("Find %s matched a secret value: %v", secret, got)
		}
	}
	// refs stay consistent with ReadPage
	if again, _ := b.ReadPage(ctx5(t), false); refNamed(t, again, "Save") != save {
		t.Error("ReadPage refs changed after Find")
	}
	if _, err := b.Find(ctx5(t), " ", 0); err == nil {
		t.Error("an empty query was accepted")
	}

	title, text, err := b.PageText(ctx5(t))
	if err != nil || title != "T" {
		t.Fatalf("PageText: %q %v", title, err)
	}
	if !strings.Contains(text, "Body text here") || !strings.Contains(text, "Title") {
		t.Errorf("main text missing: %q", text)
	}
	for _, bad := range []string{"NavLink", "FooterText", "4111", "hunter22", "4222"} {
		if strings.Contains(text, bad) {
			t.Errorf("PageText contains %q: %q", bad, text)
		}
	}
	// without a <main>, the body
	loadPage(t, b, `<nav>OnlyNav</nav><p>Plain</p>`)
	if _, text, _ := b.PageText(ctx5(t)); !strings.Contains(text, "OnlyNav") || !strings.Contains(text, "Plain") {
		t.Errorf("body text: %q", text)
	}
	loadPage(t, b, `<main>`+strings.Repeat("word ", 30000)+`</main>`)
	if _, text, _ := b.PageText(ctx5(t)); len([]rune(text)) > maxPageText+100 || !strings.Contains(text, "cut at") {
		t.Errorf("PageText is not capped: %d", len(text))
	}
	_ = snap
}

func TestEval(t *testing.T) {
	b := br(t)
	loadPage(t, b, `<input type=password id=pw aria-label=Pw value=abc><input name=card_number id=cc value=4111>
<input id=plain value=ok><button onclick="document.getElementById('out').textContent='read:'+document.getElementById('pw').value">Read</button><div id=out></div>
<iframe srcdoc="<input type=password id=ipw value=zzz>" id=fr></iframe>`)
	for js, want := range map[string]string{
		`1+1`: "2", `({a:[1,2]})`: `{"a":[1,2]}`, `'hi'`: "hi", `await Promise.resolve(5)`: "5", `undefined`: "undefined",
		`let z=1; z+1`: "2", `document.getElementById('plain').value`: "ok", `null`: "null", `1n`: "1n",
	} {
		if got, err := b.Eval(ctx5(t), js); err != nil || got != want {
			t.Errorf("Eval(%s) = %q, %v; want %q", js, got, err, want)
		}
	}
	if got := ev(t, b, `let z=10; z+1`); got != "11" {
		t.Errorf("REPL redeclaration: %q", got) // let z again
	}
	if _, err := b.Eval(ctx5(t), `throw new Error('boom')`); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("exception: %v", err)
	}
	if _, err := b.Eval(ctx5(t), `nope.x`); err == nil || !strings.Contains(err.Error(), "nope is not defined") {
		t.Errorf("reference error: %v", err)
	}
	if got := ev(t, b, `'x'.repeat(30000)`); len([]rune(got)) > maxEval+60 || !strings.Contains(got, "cut at") {
		t.Errorf("result not capped: %d", len(got))
	}
	// secret fields
	for _, js := range []string{
		`document.getElementById('pw').value`, `document.getElementById('pw').value='x'`, `document.getElementById('cc').value`,
		`document.getElementById('pw').defaultValue`, `document.querySelector('input[type=password]').value.length`,
		`document.getElementById('fr').contentDocument.getElementById('ipw').value`,
	} {
		if _, err := b.Eval(ctx5(t), js); err == nil || !strings.Contains(err.Error(), "never reads or sets password or payment fields") {
			t.Errorf("Eval(%s): %v", js, err)
		}
	}
	if got := ev(t, b, `document.getElementById('plain').value='fine'; document.getElementById('plain').value`); got != "fine" {
		t.Errorf("a plain field: %q", got)
	}
	// the page's own scripts are untouched once Eval is done
	snap, _ := b.ReadPage(ctx5(t), true)
	if _, err := b.Click(ctx5(t), refNamed(t, snap, "Read"), Allow{}); err != nil {
		t.Fatal(err)
	}
	if got := ev(t, b, `document.getElementById('out').textContent`); got != "read:abc" {
		t.Errorf("the page can no longer read its field: %q", got)
	}
	// time limit
	start := time.Now()
	if _, err := b.Eval(ctx5(t), `window.pending=new Promise(function(){}); window.pending`); err == nil || !strings.Contains(err.Error(), "10s") {
		t.Errorf("a script that never ends: %v", err)
	}
	if d := time.Since(start); d < 9*time.Second || d > 15*time.Second {
		t.Errorf("timeout after %v", d)
	}
	if got := ev(t, b, `1+2`); got != "3" {
		t.Errorf("the tab is unusable after a timeout: %q", got)
	}
	// the submit guard stays on
	if got := ev(t, b, `document.forms.length`); got != "0" {
		t.Errorf("forms: %q", got)
	}
}

func TestResizeAndWait(t *testing.T) {
	b := br(t)
	loadPage(t, b, `<meta name=viewport content="width=device-width,initial-scale=1"><p>responsive</p>`)
	defer func() { _ = b.Resize(ctx5(t), "desktop", 0, 0) }()
	for _, c := range []struct {
		preset string
		w, h   int
		want   int
		touch  string
	}{
		{"mobile", 0, 0, 390, "true"}, {"tablet", 0, 0, 820, "true"}, {"", 640, 480, 640, "false"}, {"desktop", 0, 0, 1280, "false"},
	} {
		if err := b.Resize(ctx5(t), c.preset, c.w, c.h); err != nil {
			t.Fatalf("Resize(%q): %v", c.preset, err)
		}
		if n := evNum(t, b, `window.innerWidth`); n != c.want {
			t.Errorf("%q: innerWidth = %d, want %d", c.preset, n, c.want)
		}
		if got := ev(t, b, `navigator.maxTouchPoints>0`); got != c.touch {
			t.Errorf("%q: touch = %s", c.preset, got)
		}
	}
	_ = b.Resize(ctx5(t), "mobile", 0, 0)
	if n := evNum(t, b, `window.innerHeight`); n != 844 {
		t.Errorf("mobile innerHeight = %d", n)
	}
	if s, err := b.Screenshot(ctx5(t), false, false); err != nil || s.Width != 390 || s.Height != 844 {
		t.Errorf("mobile screenshot: %+v %v", s, err)
	}
	for _, bad := range []struct {
		p    string
		w, h int
	}{{"watch", 0, 0}, {"", 10, 10}, {"custom", 9000, 100}} {
		if err := b.Resize(ctx5(t), bad.p, bad.w, bad.h); err == nil {
			t.Errorf("Resize(%q,%d,%d) was accepted", bad.p, bad.w, bad.h)
		}
	}
	start := time.Now()
	snap, err := b.Wait(ctx5(t), 400*time.Millisecond)
	if err != nil || time.Since(start) < 400*time.Millisecond || !strings.Contains(snap.Tree, "responsive") {
		t.Errorf("Wait: %v %v", err, snap)
	}
	// a cancelled call ends the wait
	c, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := b.Wait(c, 30*time.Second); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("a cancelled Wait: %v after %v", err, time.Since(start))
	}
}

func TestTabs(t *testing.T) {
	b := br(t)
	a, b2 := serve(t, html(`<h1>Tab A</h1><button>BtnA</button>`)), serve(t, html(`<h1>Tab B</h1><button>BtnB</button>`))
	if _, err := b.Navigate(ctx5(t), a.URL); err != nil {
		t.Fatal(err)
	}
	ev(t, b, `console.error('from-A')`)
	base := targetCount(t, b)
	tabs, err := b.Tabs(ctx5(t))
	if err != nil || len(tabs) != 1 || !tabs[0].Active || !strings.HasPrefix(tabs[0].URL, a.URL) || tabs[0].Title != "T" {
		t.Fatalf("Tabs: %+v, %v", tabs, err)
	}
	first := tabs[0].ID

	snap, err := b.NewTab(ctx5(t), b2.URL)
	if err != nil || !strings.Contains(snap.Tree, "Tab B") {
		t.Fatalf("NewTab: %v %+v", err, snap)
	}
	if n := targetCount(t, b); n != base+1 {
		t.Errorf("page targets = %d, want %d", n, base+1)
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 2 || tabs[0].Active || !tabs[1].Active || !strings.HasPrefix(tabs[1].URL, b2.URL) {
		t.Fatalf("Tabs after NewTab: %+v", tabs)
	}
	second := tabs[1].ID
	// console, network, refs and the guard follow the active tab
	for _, c := range b.Console(true, false) {
		if strings.Contains(c.Text, "from-A") {
			t.Error("tab A's console entries show in tab B")
		}
	}
	ev(t, b, `console.error('from-B')`)
	snapB, _ := b.ReadPage(ctx5(t), true)
	refB := refNamed(t, snapB, "BtnB")
	if snap, err = b.SwitchTab(ctx5(t), first); err != nil || !strings.HasPrefix(snap.URL, a.URL) || !strings.Contains(snap.Tree, "BtnA") {
		t.Fatalf("SwitchTab: %v %+v", err, snap)
	}
	if got := b.Console(true, false); len(got) != 1 || !strings.Contains(got[0].Text, "from-A") {
		t.Errorf("console after switching back: %+v", got)
	}
	_ = refB // refs are per tab
	if reqs := b.Network(false, false, b2.URL); len(reqs) != 0 {
		t.Errorf("tab B's requests show in tab A: %+v", reqs)
	}
	if _, err := b.NewTab(ctx5(t), "about:blank"); err != nil {
		t.Fatal(err)
	}
	if third, _ := b.Tabs(ctx5(t)); len(third) != 3 {
		t.Fatalf("tabs: %+v", third)
	}

	if err := b.CloseTab(ctx5(t), "nonexistent"); err == nil {
		t.Error("closing an unknown tab worked")
	}
	if _, err := b.SwitchTab(ctx5(t), "nonexistent"); err == nil {
		t.Error("switching to an unknown tab worked")
	}
	// close the first (root) tab while others live: the rest keeps working
	if err := b.CloseTab(ctx5(t), first); err != nil {
		t.Fatal(err)
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 2 {
		t.Fatalf("tabs after closing the first: %+v", tabs)
	}
	if snap, err := b.SwitchTab(ctx5(t), second); err != nil || !strings.Contains(snap.Tree, "Tab B") {
		t.Fatalf("switch after the first tab closed: %v", err)
	}
	if snap, err := b.NewTab(ctx5(t), a.URL); err != nil || !strings.Contains(snap.Tree, "Tab A") {
		t.Fatalf("NewTab after the first tab closed: %v", err)
	}
	// closing the active tab moves to another
	tabs, _ = b.Tabs(ctx5(t))
	for _, ti := range tabs {
		if ti.ID != second {
			if err := b.CloseTab(ctx5(t), ti.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 1 || tabs[0].ID != second || !tabs[0].Active {
		t.Fatalf("tabs: %+v", tabs)
	}
	// closing the last tab opens a blank one
	if err := b.CloseTab(ctx5(t), second); err != nil {
		t.Fatal(err)
	}
	tabs, _ = b.Tabs(ctx5(t))
	if len(tabs) != 1 || tabs[0].ID == second || !tabs[0].Active || tabs[0].URL != "about:blank" {
		t.Fatalf("tabs after closing the last: %+v", tabs)
	}
	eventually(t, "the closed tabs to be gone", 5*time.Second, func() bool { return targetCount(t, b) == 1 })
	// and the browser still works
	if snap, err := b.Navigate(ctx5(t), a.URL); err != nil || !strings.Contains(snap.Tree, "Tab A") {
		t.Fatalf("navigate after all that: %v", err)
	}
	if !b.Alive() {
		t.Error("not alive")
	}
	for i := 0; i < maxTabs; i++ {
		if _, err := b.NewTab(ctx5(t), ""); err != nil {
			if i < maxTabs-1 {
				t.Fatalf("NewTab %d: %v", i, err)
			}
			break
		}
	}
	if _, err := b.NewTab(ctx5(t), ""); err == nil {
		t.Error("the tab limit was not enforced")
	}
	tabs, _ = b.Tabs(ctx5(t))
	for _, ti := range tabs[1:] {
		if err := b.CloseTab(ctx5(t), ti.ID); err != nil {
			t.Fatal(err)
		}
	}
	if tabs, _ = b.Tabs(ctx5(t)); len(tabs) != 1 {
		t.Errorf("leftover tabs: %+v", tabs)
	}
}

func TestPopupsAreRewritten(t *testing.T) {
	b := br(t)
	denied, dc := countingServer(t, `<h1>Denied</h1>`)
	ok, _ := countingServer(t, `<h1>Allowed</h1>`)
	s := serve(t, html(`<a href="`+denied.URL+`/a" target=_blank>Blank denied</a>
<a href="`+ok.URL+`/ok" target=popupwin>Named allowed</a>
<button onclick="window.open('`+denied.URL+`/b','w','width=300')">Open denied</button>
<button onclick="var r=window.open('`+ok.URL+`/ok2');document.title='ret:'+r">Open allowed</button>
<form action="`+denied.URL+`/c" target=_blank method=get><button>Form denied</button></form>
<form action="`+denied.URL+`/d" method=get><button formtarget=_blank>Formtarget denied</button></form>`))
	allowHook.Store(func(h string) bool { return h == hostOf(s.URL) || h == hostOf(ok.URL) })
	base := targetCount(t, b)
	fresh := func() string {
		snap, err := b.Navigate(ctx5(t), s.URL)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Tree
	}
	for _, name := range []string{"Blank denied", "Open denied"} {
		tree := fresh()
		got, err := b.Click(ctx5(t), refNamed(t, tree, name), Allow{})
		if err != nil || len(got.Blocked) == 0 || !strings.Contains(got.Blocked[0], denied.URL) || !strings.HasPrefix(got.URL, s.URL) {
			t.Fatalf("%s: %v %+v", name, err, got)
		}
	}
	for _, name := range []string{"Form denied", "Formtarget denied"} {
		tree := fresh()
		got, err := b.Click(ctx5(t), refNamed(t, tree, name), Allow{Submit: true})
		if err != nil || !hasNote(got.Blocked, denied.URL) {
			t.Fatalf("%s: %v %+v", name, err, got)
		}
	}
	tree := fresh()
	got, err := b.Click(ctx5(t), refNamed(t, tree, "Named allowed"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, ok.URL+"/ok") {
		t.Fatalf("named target to an allowed host: %v %+v", err, got)
	}
	tree = fresh()
	got, err = b.Click(ctx5(t), refNamed(t, tree, "Open allowed"), Allow{})
	if err != nil || !strings.HasPrefix(got.URL, ok.URL+"/ok2") {
		t.Fatalf("window.open to an allowed host: %v %+v", err, got)
	}
	if n := dc.n("GET /a") + dc.n("GET /b") + dc.n("GET /c") + dc.n("GET /d"); n != 0 {
		t.Errorf("the denied host received %d requests", n)
	}
	time.Sleep(popupWait + 300*time.Millisecond)
	if n := targetCount(t, b); n != base {
		t.Errorf("page targets = %d, want %d: a popup opened", n, base)
	}
}

func TestParseKeys(t *testing.T) {
	good := map[string]int{"Tab": 1, "ctrl+a": 1, "cmd+shift+k": 1, "shift+Tab Enter": 2, "F5 f12": 2, "ctrl++": 1, "+": 1, "A": 1, "Return up": 2, "1 -": 2}
	for in, n := range good {
		seq, err := parseKeys(in)
		if err != nil || len(seq) != n {
			t.Errorf("parseKeys(%q) = %v, %v", in, seq, err)
		}
	}
	seq, _ := parseKeys("cmd+shift+k")
	if seq[0].mods != modMeta|modShift || seq[0].k.key != "K" || seq[0].k.code != "KeyK" || seq[0].k.vk != 75 {
		t.Errorf("cmd+shift+k = %+v", seq[0])
	}
	if seq, _ := parseKeys("A"); seq[0].mods != modShift || seq[0].k.key != "A" {
		t.Errorf("A = %+v", seq[0])
	}
	if seq, _ := parseKeys("ctrl++"); seq[0].k.key != "+" || seq[0].mods != modCtrl {
		t.Errorf("ctrl++ = %+v", seq[0])
	}
	for _, bad := range []string{"", "F13", "Foo", "hyper+a", "ctrl+"} {
		if _, err := parseKeys(bad); err == nil {
			t.Errorf("parseKeys(%q) was accepted", bad)
		}
	}
}
