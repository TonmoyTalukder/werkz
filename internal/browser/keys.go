package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	maxKeyPresses = 500
	errSecretType = "werkz never types into password or payment fields"
)

const (
	modAlt   = 1
	modCtrl  = 2
	modMeta  = 4
	modShift = 8
)

// keyDef is one physical key as the page sees it in a KeyboardEvent.
type keyDef struct {
	key, code string
	vk        int64
	text      string // what it types without modifiers
}

var namedKeys = map[string]keyDef{
	"enter": {"Enter", "Enter", 13, "\r"}, "tab": {"Tab", "Tab", 9, ""}, "escape": {"Escape", "Escape", 27, ""},
	"backspace": {"Backspace", "Backspace", 8, ""}, "delete": {"Delete", "Delete", 46, ""},
	"space": {" ", "Space", 32, " "}, "insert": {"Insert", "Insert", 45, ""},
	"arrowup": {"ArrowUp", "ArrowUp", 38, ""}, "arrowdown": {"ArrowDown", "ArrowDown", 40, ""},
	"arrowleft": {"ArrowLeft", "ArrowLeft", 37, ""}, "arrowright": {"ArrowRight", "ArrowRight", 39, ""},
	"home": {"Home", "Home", 36, ""}, "end": {"End", "End", 35, ""},
	"pageup": {"PageUp", "PageUp", 33, ""}, "pagedown": {"PageDown", "PageDown", 34, ""},
}

var keyAliases = map[string]string{
	"return": "enter", "esc": "escape", "del": "delete", "up": "arrowup", "down": "arrowdown", "left": "arrowleft",
	"right": "arrowright", "pgup": "pageup", "pgdn": "pagedown", "ins": "insert",
}

var punctKeys = map[rune]struct {
	code string
	vk   int64
}{
	'-': {"Minus", 189}, '=': {"Equal", 187}, '[': {"BracketLeft", 219}, ']': {"BracketRight", 221},
	'\\': {"Backslash", 220}, ';': {"Semicolon", 186}, '\'': {"Quote", 222}, ',': {"Comma", 188},
	'.': {"Period", 190}, '/': {"Slash", 191}, '`': {"Backquote", 192},
}

// pressed is one parsed key or chord.
type pressed struct {
	k    keyDef
	mods int64
	raw  string
}

func (p pressed) has(m int64) bool { return p.mods&m != 0 }

// printable: the key would put a character into a field.
func (p pressed) inserts() bool {
	if p.k.text == "" || p.k.key == "Enter" {
		return false
	}
	return !p.has(modCtrl) && !p.has(modMeta) || p.has(modCtrl) && p.has(modAlt) // AltGr
}

// pastes: the chord would put the clipboard into a field.
func (p pressed) pastes() bool {
	k := strings.ToLower(p.k.key)
	return (p.has(modCtrl) || p.has(modMeta)) && k == "v" || p.has(modShift) && k == "insert"
}

// reads: copy and cut would move a field's text to the clipboard.
func (p pressed) reads() bool {
	k := strings.ToLower(p.k.key)
	return (p.has(modCtrl) || p.has(modMeta)) && (k == "c" || k == "x")
}

func lookupKey(name string, shift bool) (keyDef, error) {
	if r := []rune(name); len(r) == 1 {
		c := r[0]
		switch {
		case unicode.IsLetter(c) && c < 128:
			up := unicode.ToUpper(c)
			k := keyDef{key: string(c), code: "Key" + string(up), vk: int64(up), text: string(c)}
			if shift {
				k.key, k.text = string(up), string(up)
			}
			return k, nil
		case c >= '0' && c <= '9':
			return keyDef{key: string(c), code: "Digit" + string(c), vk: int64(c), text: string(c)}, nil
		case c == ' ':
			return namedKeys["space"], nil
		}
		if p, ok := punctKeys[c]; ok {
			return keyDef{key: string(c), code: p.code, vk: p.vk, text: string(c)}, nil
		}
		return keyDef{key: string(c), text: string(c)}, nil // any other character is typed as is
	}
	l := strings.ToLower(name)
	if a, ok := keyAliases[l]; ok {
		l = a
	}
	if k, ok := namedKeys[l]; ok {
		return k, nil
	}
	if len(l) >= 2 && l[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(l[1:], "%d", &n); err == nil && n >= 1 && n <= 12 && fmt.Sprint(n) == l[1:] {
			return keyDef{key: fmt.Sprintf("F%d", n), code: fmt.Sprintf("F%d", n), vk: int64(111 + n)}, nil
		}
	}
	return keyDef{}, fmt.Errorf("unknown key %q (use names such as Enter, Tab, Escape, Backspace, ArrowDown, F5, a single character, or a chord such as ctrl+a)", name)
}

// parseKeys splits "ctrl+a Tab shift+Tab" into presses.
func parseKeys(keys string) ([]pressed, error) {
	var out []pressed
	for _, tok := range strings.Fields(keys) {
		var mods int64
		name := tok
		for len(name) > 1 {
			i := strings.Index(name, "+")
			if i <= 0 {
				break
			}
			head := name[:i]
			bit, ok := modBits[strings.ToLower(head)]
			if !ok {
				return nil, fmt.Errorf("unknown modifier %q in %q (use ctrl, alt, shift, cmd/meta)", head, tok)
			}
			mods |= bit
			name = name[i+1:]
			if name == "" {
				return nil, fmt.Errorf("%q has no key after the modifiers", tok)
			}
		}
		k, err := lookupKey(name, mods&modShift != 0)
		if err != nil {
			return nil, err
		}
		if len([]rune(name)) == 1 && unicode.IsUpper([]rune(name)[0]) {
			mods |= modShift
		}
		out = append(out, pressed{k: k, mods: mods, raw: tok})
	}
	if len(out) == 0 {
		return nil, errors.New("no keys given")
	}
	return out, nil
}

var modKeys = []struct {
	bit  int64
	key  string
	code string
	vk   int64
}{
	{modCtrl, "Control", "ControlLeft", 17}, {modAlt, "Alt", "AltLeft", 18},
	{modShift, "Shift", "ShiftLeft", 16}, {modMeta, "Meta", "MetaLeft", 91},
}

// press sends the modifier downs, the key down and up, and the modifier ups.
func press(ctx context.Context, p pressed) error {
	send := func(e input.DispatchKeyEventParams) error {
		_, err := chromedp.Call(ctx, input.DispatchKeyEvent, e)
		return err
	}
	var cum int64
	var held []int
	for i, m := range modKeys {
		if p.mods&m.bit == 0 {
			continue
		}
		cum |= m.bit
		held = append(held, i)
		if err := send(input.DispatchKeyEventParams{
			Type: input.DispatchKeyEventTypeRawKeyDown, Key: m.key, Code: m.code,
			WindowsVirtualKeyCode: m.vk, NativeVirtualKeyCode: m.vk, Modifiers: cum, Location: 1,
		}); err != nil {
			return err
		}
	}
	down := input.DispatchKeyEventParams{
		Type: input.DispatchKeyEventTypeRawKeyDown, Key: p.k.key, Code: p.k.code,
		WindowsVirtualKeyCode: p.k.vk, NativeVirtualKeyCode: p.k.vk, Modifiers: p.mods,
	}
	if p.k.text != "" && !p.has(modCtrl) && !p.has(modMeta) && !p.has(modAlt) || p.inserts() {
		down.Type, down.Text, down.UnmodifiedText = input.DispatchKeyEventTypeKeyDown, p.k.text, p.k.text
	}
	// Meta chords carry the editing command: Chrome on Linux and Windows has
	// no binding for them, and on macOS the command is how it is told.
	if p.has(modMeta) && !p.has(modCtrl) && !p.has(modAlt) {
		switch strings.ToLower(p.k.key) {
		case "a":
			down.Commands = []string{"selectAll"}
		case "c":
			down.Commands = []string{"copy"}
		case "x":
			down.Commands = []string{"cut"}
		case "v":
			down.Commands = []string{"paste"}
		case "z":
			down.Commands = []string{"undo"}
			if p.has(modShift) {
				down.Commands = []string{"redo"}
			}
		}
	}
	up := down
	up.Type, up.Text, up.UnmodifiedText, up.Commands = input.DispatchKeyEventTypeKeyUp, "", "", nil
	if err := send(down); err != nil {
		return err
	}
	if err := send(up); err != nil {
		return err
	}
	for i := len(held) - 1; i >= 0; i-- {
		m := modKeys[held[i]]
		cum &^= m.bit
		if err := send(input.DispatchKeyEventParams{
			Type: input.DispatchKeyEventTypeKeyUp, Key: m.key, Code: m.code,
			WindowsVirtualKeyCode: m.vk, NativeVirtualKeyCode: m.vk, Modifiers: cum, Location: 1,
		}); err != nil {
			return err
		}
	}
	return nil
}

// focusExpr finds the focused element, through shadow roots and same-origin
// iframes.
const focusExpr = `(function(){
  var a=document.activeElement;
  for(var i=0;i<30&&a;i++){
    if(a.shadowRoot&&a.shadowRoot.activeElement){a=a.shadowRoot.activeElement;continue}
    if(a.tagName==='IFRAME'||a.tagName==='FRAME'){try{var d=a.contentDocument;if(d&&d.activeElement){a=d.activeElement;continue}}catch(e){}}
    break;
  }
  return a||document.body;
})()`

const focusInfoJS = `function(){
  var el=this,g=function(n){return (el.getAttribute&&el.getAttribute(n))||''};
  return {tag:String(el.tagName||'').toLowerCase(),type:(typeof el.type==='string'?el.type:g('type')).toLowerCase(),
    autocomplete:g('autocomplete'),name:g('name'),id:el.id||''};
}`

type focusInfo struct {
	Tag, Type, Autocomplete, Name, ID string
}

func (f focusInfo) secret() bool { return secretField(f.Tag, f.Type, f.Autocomplete, f.Name, f.ID) }
func (f focusInfo) nothing() bool {
	return f.Tag == "" || f.Tag == "body" || f.Tag == "html"
}

// focusObject returns the focused element as a remote object.
func focusObject(ctx context.Context) (runtime.RemoteObjectID, error) {
	r, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: focusExpr})
	if err != nil {
		return "", err
	}
	if r.ExceptionDetails != nil || r.Result == nil || r.Result.ObjectID == "" {
		return "", errors.New("could not find the focused element")
	}
	return r.Result.ObjectID, nil
}

func activeInfo(ctx context.Context) (focusInfo, error) {
	var f focusInfo
	obj, err := focusObject(ctx)
	if err != nil {
		return f, err
	}
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{FunctionDeclaration: focusInfoJS, ObjectID: obj, ReturnByValue: new(true)})
	if err != nil {
		return f, err
	}
	if res.Result == nil {
		return f, errors.New("could not check the focus")
	}
	return f, json.Unmarshal(res.Result.Value, &f)
}

// FocusedElement describes the element that has the focus now (through shadow
// roots and same-origin iframes). With nothing focused it is the page body.
func (b *Browser) FocusedElement(call context.Context) (*Element, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	obj, err := focusObject(ctx)
	if err != nil {
		return nil, err
	}
	return b.describeObject(ctx, obj, "")
}

// Key presses keys: space-separated names, characters or chords ("Tab",
// "Escape", "Enter", "ArrowDown", "Backspace", "ctrl+a", "cmd+shift+k",
// "shift+Tab", "a"; cmd is Meta), the whole sequence repeat times (default 1).
// Keys that would put text into a password or payment field (characters,
// paste, copy and cut chords) are refused: the focus is checked before each.
// Enter or space on a submit control, or Enter in a form field, submits the
// form: that is blocked (and reported in Snapshot.Blocked) unless
// allow.Submit.
func (b *Browser) Key(call context.Context, keys string, repeat int, allow Allow) (*Snapshot, error) {
	seq, err := parseKeys(keys)
	if err != nil {
		return nil, err
	}
	if repeat <= 0 {
		repeat = 1
	}
	if len(seq)*repeat > maxKeyPresses {
		return nil, fmt.Errorf("too many key presses (%d): the limit is %d", len(seq)*repeat, maxKeyPresses)
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	end, err := b.startAllow(ctx, allow)
	if err != nil {
		return nil, err
	}
	defer end()
	for r := 0; r < repeat; r++ {
		for _, p := range seq {
			if p.inserts() || p.pastes() || p.reads() {
				if err := refuseSecretFocus(ctx); err != nil {
					return nil, err
				}
			}
			if err := press(ctx, p); err != nil {
				return nil, err
			}
		}
	}
	b.settle(ctx, 5*time.Second)
	end()
	return b.snapshot(ctx)
}

func refuseSecretFocus(ctx context.Context) error {
	f, err := activeInfo(ctx)
	if err != nil {
		return err
	}
	if f.secret() {
		return errors.New(errSecretType)
	}
	return nil
}

// TypeText types text into whatever has the focus, as a user does after
// clicking a field: the text is inserted at once, and each newline is an Enter
// key press (which submits a form unless blocked: allow.Submit). The focus is
// checked first, through shadow roots and same-origin iframes: a password or
// payment field is refused, and so is a page with nothing focused.
func (b *Browser) TypeText(call context.Context, text string, allow Allow) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	f, err := activeInfo(ctx)
	if err != nil {
		return nil, err
	}
	if f.secret() {
		return nil, errors.New(errSecretType)
	}
	if f.nothing() {
		return nil, errors.New("nothing has focus: click a field first")
	}
	end, err := b.startAllow(ctx, allow)
	if err != nil {
		return nil, err
	}
	defer end()
	enter, _ := lookupKey("Enter", false)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			if err := refuseSecretFocus(ctx); err != nil {
				return nil, err
			}
			if err := press(ctx, pressed{k: enter}); err != nil {
				return nil, err
			}
		}
		if line == "" {
			continue
		}
		if i > 0 {
			if err := refuseSecretFocus(ctx); err != nil {
				return nil, err
			}
		}
		if _, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: line}); err != nil {
			return nil, err
		}
	}
	b.settle(ctx, 5*time.Second)
	end()
	return b.snapshot(ctx)
}
