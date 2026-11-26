package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const maxReadPage = 60000 // ReadPage cap; Snapshot.Tree uses the smaller maxTreeSnap

// refInfo is what a ref points at, as of the last ReadPage.
type refInfo struct {
	backend    cdp.BackendNodeID
	role, name string
}

var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true, "checkbox": true, "radio": true,
	"combobox": true, "comboboxselect": true, "comboboxmenubutton": true, "listbox": true, "option": true,
	"menuitem": true, "menuitemcheckbox": true, "menuitemradio": true, "tab": true, "switch": true,
	"slider": true, "spinbutton": true, "treeitem": true, "disclosuretriangle": true, "popupbutton": true,
}

// textRoles hold a user-entered value, which we show (except for secrets).
var textRoles = map[string]bool{"textbox": true, "searchbox": true, "combobox": true, "comboboxselect": true, "spinbutton": true}

// passRoles add no information of their own: their children are shown in
// their place.
var passRoles = map[string]bool{
	"generic": true, "none": true, "presentation": true, "genericcontainer": true, "paragraph": true,
	"rootwebarea": true, "webarea": true, "labeltext": true, "section": true, "group": true,
	"list": true, "listitem": true, "row": true, "rowgroup": true, "cell": true, "gridcell": true,
	"columnheader": true, "rowheader": true, "div": true,
}

var skipRoles = map[string]bool{"inlinetextbox": true, "linebreak": true, "listmarker": true}

func axStr(v *accessibility.Value) string {
	if v == nil || len(v.Value) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(v.Value, &s) == nil {
		return s
	}
	return strings.Trim(string(v.Value), `"`)
}

func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// ReadPage returns the accessibility tree as text. Refs in it are valid until
// the next ReadPage or Navigate.
func (b *Browser) ReadPage(call context.Context, interactiveOnly bool) (string, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return "", err
	}
	defer done()
	lines, err := b.treeLines(ctx, interactiveOnly, true)
	if err != nil {
		return "", err
	}
	return capLines(lines, maxReadPage), nil
}

// treeLines builds the tree and assigns refs. reset starts numbering over;
// otherwise nodes seen before keep their ref, so the refs a Snapshot shows
// stay usable for follow-up actions.
func (b *Browser) treeLines(ctx context.Context, interactiveOnly, reset bool) ([]string, error) {
	res, err := chromedp.Call(ctx, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return nil, fmt.Errorf("could not read the page: %w", err)
	}
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(res.Nodes))
	var roots []*accessibility.Node
	for _, n := range res.Nodes {
		byID[n.NodeID] = n
		if n.ParentID == "" {
			roots = append(roots, n)
		}
	}
	b.mu.Lock()
	origin := b.lastURL
	b.mu.Unlock()
	pageURL, _ := url.Parse(origin)

	b.mu.Lock()
	if reset || b.refs == nil {
		b.refs, b.refNode = map[string]refInfo{}, map[cdp.BackendNodeID]string{}
	}
	refs, refNode := b.refs, b.refNode
	b.mu.Unlock()
	var lines []string
	var walk func(n *accessibility.Node, depth int, parent string)
	walk = func(n *accessibility.Node, depth int, parent string) {
		role := strings.ToLower(axStr(n.Role))
		name := clean(axStr(n.Name))
		kids := func(d int, p string) {
			for _, c := range n.ChildIDs {
				if cn := byID[c]; cn != nil {
					walk(cn, d, p)
				}
			}
		}
		if n.Ignored || skipRoles[role] || (passRoles[role] && !(role == "group" || role == "section")) ||
			((role == "group" || role == "section") && name == "") {
			kids(depth, parent)
			return
		}
		props := map[string]string{}
		for _, p := range n.Properties {
			props[string(p.Name)] = axStr(p.Value)
		}
		interactive := interactiveRoles[role] || props["focusable"] == "true" && role != "statictext"
		if role == "statictext" {
			if name != "" && !interactiveOnly && !strings.Contains(parent, name) {
				lines = append(lines, strings.Repeat("  ", depth)+"text "+strconv.Quote(name))
			}
			return
		}
		if role == "image" || role == "img" {
			if name == "" || interactiveOnly {
				kids(depth, parent)
				return
			}
		}
		if interactiveOnly && !interactive {
			kids(depth, parent)
			return
		}
		d := depth
		if interactiveOnly {
			d = 0
		}
		var sb strings.Builder
		sb.WriteString(strings.Repeat("  ", d))
		if n.BackendDOMNodeID != 0 && (interactive || role == "heading" || role == "image" || role == "img") {
			b.mu.Lock()
			ref, ok := refNode[n.BackendDOMNodeID]
			if !ok {
				ref = "ref_" + strconv.Itoa(len(refs)+1)
				refNode[n.BackendDOMNodeID] = ref
			}
			refs[ref] = refInfo{backend: n.BackendDOMNodeID, role: role, name: name}
			b.mu.Unlock()
			sb.WriteString("[" + ref + "] ")
		}
		sb.WriteString(role)
		if name != "" {
			sb.WriteString(" " + strconv.Quote(name))
		}
		if role == "link" {
			if href := props["url"]; href != "" {
				sb.WriteString(" → " + shortURL(href, pageURL))
			}
		}
		if role == "heading" && props["level"] != "" {
			sb.WriteString(" level=" + props["level"])
		}
		if textRoles[role] {
			secret := b.isSecretNode(ctx, n.BackendDOMNodeID)
			if v := axStr(n.Value); v != "" && !secret {
				sb.WriteString(" value=" + strconv.Quote(clean(v)))
			}
			if secret {
				sb.WriteString(" (secret field, never typed or shown)")
			}
		}
		switch props["checked"] {
		case "true":
			sb.WriteString(" checked")
		case "false":
			sb.WriteString(" unchecked")
		case "mixed":
			sb.WriteString(" mixed")
		}
		for _, flag := range []string{"disabled", "required", "selected", "pressed"} {
			if v := props[flag]; v == "true" {
				sb.WriteString(" " + flag)
			}
		}
		switch props["expanded"] {
		case "true":
			sb.WriteString(" expanded")
		case "false":
			sb.WriteString(" collapsed")
		}
		lines = append(lines, sb.String())
		kids(d+1, name)
	}
	for _, r := range roots {
		walk(r, 0, "")
	}
	if len(lines) == 0 {
		lines = []string{"(the page has no readable content yet)"}
	}
	return lines, nil
}

// shortURL shows same-origin links as paths.
func shortURL(href string, page *url.URL) string {
	u, err := url.Parse(href)
	if err != nil || page == nil || u.Host != page.Host || u.Scheme != page.Scheme {
		return href
	}
	return u.RequestURI() + map[bool]string{true: "#" + u.Fragment}[u.Fragment != ""]
}

var cardName = regexp.MustCompile(`(?i)card[-_ ]?(number|num|no)\b|\bcvc\b|\bcvv\b|cc[-_]?(num|number|csc|exp)`)

// secretField reports password and payment fields: the ones werkz never types
// into and never prints the value of.
func secretField(tag, typ, autocomplete, name, id string) bool {
	if tag != "input" && tag != "textarea" {
		return false
	}
	if strings.EqualFold(typ, "password") {
		return true
	}
	for _, tok := range strings.Fields(strings.ToLower(autocomplete)) {
		switch tok {
		case "current-password", "new-password", "one-time-code", "cc-number", "cc-csc", "cc-exp", "cc-exp-month", "cc-exp-year":
			return true
		}
	}
	return cardName.MatchString(name) || cardName.MatchString(id)
}

// isSecretNode asks the DOM for the attributes of one input.
func (b *Browser) isSecretNode(ctx context.Context, id cdp.BackendNodeID) bool {
	if id == 0 {
		return false
	}
	res, err := chromedp.Call(ctx, dom.DescribeNode, dom.DescribeNodeParams{BackendNodeID: id})
	if err != nil || res.Node == nil {
		return false
	}
	attrs := map[string]string{}
	for i := 0; i+1 < len(res.Node.Attributes); i += 2 {
		attrs[strings.ToLower(res.Node.Attributes[i])] = res.Node.Attributes[i+1]
	}
	return secretField(strings.ToLower(res.Node.NodeName), attrs["type"], attrs["autocomplete"], attrs["name"], attrs["id"])
}

// Element describes what a ref points at, so the caller can decide to ask the
// user first.
type Element struct {
	Ref, Role, Name, Tag, Type string
	Password                   bool // password, one-time-code or card number/cvc/expiry field
	FileInput                  bool
	Submits                    bool // clicking it submits a form
	InForm                     bool
	Href                       string // absolute link target, if a link
	Download                   bool   // <a download>
}

func staleErr(ref string) error {
	return fmt.Errorf("%s is stale: call ReadPage again", ref)
}

func (b *Browser) lookup(ref string) (refInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.refs[ref]
	if !ok {
		return refInfo{}, staleErr(ref)
	}
	return info, nil
}

// nodeGone turns the protocol's "node not found" family into a stale-ref error.
func nodeGone(ref string, err error) error {
	m := err.Error()
	if strings.Contains(m, "find node") || strings.Contains(m, "No node") || strings.Contains(m, "does not belong") || strings.Contains(m, "not found") {
		return staleErr(ref)
	}
	return err
}

const inspectJS = `function(){
  const el=this, tag=(el.tagName||'').toLowerCase();
  const a=n=>(el.getAttribute&&el.getAttribute(n))||'';
  const link=el.closest?el.closest('a[href]'):null;
  const lab=(tag==='label')?el:(el.closest?el.closest('label'):null);
  const btn=(lab&&lab.control)?lab.control:(el.closest?(el.closest('button')||el):el);
  return {tag, type:(typeof el.type==='string'?el.type:a('type')).toLowerCase(),
    autocomplete:a('autocomplete'), name:a('name'), id:el.id||'',
    inForm:!!(el.form||(el.closest&&el.closest('form'))),
    btnTag:(btn.tagName||'').toLowerCase(), btnType:(typeof btn.type==='string'?btn.type:'').toLowerCase(), btnForm:!!btn.form,
    href:(link&&typeof link.href==='string')?link.href:'', download:!!(link&&link.hasAttribute('download'))};
}`

func (b *Browser) inspect(ctx context.Context, ref string) (*Element, error) {
	info, err := b.lookup(ref)
	if err != nil {
		return nil, err
	}
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: info.backend})
	if err != nil || obj.Object == nil {
		if err == nil {
			err = errors.New("not found")
		}
		return nil, nodeGone(ref, err)
	}
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: inspectJS, ObjectID: obj.Object.ObjectID, ReturnByValue: new(true),
	})
	if err != nil {
		return nil, nodeGone(ref, err)
	}
	if res.ExceptionDetails != nil || res.Result == nil {
		return nil, fmt.Errorf("could not inspect %s", ref)
	}
	var f struct {
		Tag, Type, Autocomplete, Name, ID, BtnTag, BtnType, Href string
		InForm, BtnForm, Download                                bool
	}
	if err := json.Unmarshal(res.Result.Value, &f); err != nil {
		return nil, fmt.Errorf("could not inspect %s: %w", ref, err)
	}
	return &Element{
		Ref: ref, Role: info.role, Name: info.name, Tag: f.Tag, Type: f.Type,
		Password:  secretField(f.Tag, f.Type, f.Autocomplete, f.Name, f.ID),
		FileInput: f.Tag == "input" && f.Type == "file",
		Submits: f.BtnForm && ((f.BtnTag == "button" && f.BtnType == "submit") ||
			(f.BtnTag == "input" && (f.BtnType == "submit" || f.BtnType == "image"))),
		InForm: f.InForm, Href: f.Href, Download: f.Download,
	}, nil
}

// Inspect reports what a ref points at, without touching the page.
func (b *Browser) Inspect(call context.Context, ref string) (*Element, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	return b.inspect(ctx, ref)
}

// containsJS is true when the hit element is this element, inside it
// (shadow trees included), or a label that activates it.
const containsJS = `function(h){
  for(let n=h,i=0;n&&i<500;i++,n=n.parentNode||n.host){if(n===this)return true;}
  return !!(h&&h.tagName==='LABEL'&&h.control===this);
}`

// describeNode names an element for an error message: <tag#id>.
func (b *Browser) describeNode(ctx context.Context, id cdp.BackendNodeID) string {
	res, err := chromedp.Call(ctx, dom.DescribeNode, dom.DescribeNodeParams{BackendNodeID: id})
	if err != nil || res.Node == nil {
		return "unknown element"
	}
	s := strings.ToLower(res.Node.NodeName)
	for i := 0; i+1 < len(res.Node.Attributes); i += 2 {
		if res.Node.Attributes[i] == "id" && res.Node.Attributes[i+1] != "" {
			s += "#" + res.Node.Attributes[i+1]
		}
	}
	return "<" + s + ">"
}

// center returns the click point of a node, after scrolling it into view,
// and checks that the point really lands on the node (or inside it) rather
// than on an overlay.
func (b *Browser) center(ctx context.Context, ref string, id cdp.BackendNodeID) (x, y float64, err error) {
	if _, err = chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: id}); err != nil {
		return 0, 0, nodeGone(ref, err)
	}
	q, err := chromedp.Call(ctx, dom.GetContentQuads, dom.GetContentQuadsParams{BackendNodeID: id})
	if err != nil {
		return 0, 0, nodeGone(ref, err)
	}
	found := false
	for _, quad := range q.Quads {
		if len(quad) >= 8 {
			x, y, found = (quad[0]+quad[2]+quad[4]+quad[6])/4, (quad[1]+quad[3]+quad[5]+quad[7])/4, true
			break
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("%s is not visible on the page, so it cannot be clicked", ref)
	}
	// Re-resolve right before clicking: the node may have been replaced.
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: id})
	if err != nil || obj.Object == nil {
		if err == nil {
			err = errors.New("not found")
		}
		return 0, 0, nodeGone(ref, err)
	}
	hit, err := chromedp.Call(ctx, dom.GetNodeForLocation, dom.GetNodeForLocationParams{X: int64(x), Y: int64(y)})
	if err != nil {
		return 0, 0, fmt.Errorf("%s is not visible at its click point: ReadPage again or close the overlay", ref)
	}
	hitObj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: hit.BackendNodeID})
	if err != nil || hitObj.Object == nil {
		return 0, 0, fmt.Errorf("%s is covered by another element: ReadPage again or close the overlay", ref)
	}
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: containsJS, ObjectID: obj.Object.ObjectID, ReturnByValue: new(true),
		Arguments: []*runtime.CallArgument{{ObjectID: hitObj.Object.ObjectID}},
	})
	if err != nil {
		return 0, 0, nodeGone(ref, err)
	}
	if res.Result == nil || string(res.Result.Value) != "true" {
		return 0, 0, fmt.Errorf("%s is covered by another element (%s): ReadPage again or close the overlay", ref, b.describeNode(ctx, hit.BackendNodeID))
	}
	return x, y, nil
}

// Click clicks the centre of the element with real mouse events. The point is
// checked first: if another element is on top of it, nothing is clicked.
// allow says what the click may do; without Allow.Submit any form submission
// it causes is blocked and reported in Snapshot.Blocked.
func (b *Browser) Click(call context.Context, ref string, allow Allow) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	info, err := b.lookup(ref)
	if err != nil {
		return nil, err
	}
	end, err := b.startAllow(ctx, allow)
	if err != nil {
		return nil, err
	}
	defer end()
	x, y, err := b.center(ctx, ref, info.backend)
	if err != nil {
		return nil, err
	}
	for _, ev := range []input.DispatchMouseEventParams{
		{Type: input.DispatchMouseEventTypeMouseMoved, X: x, Y: y},
		{Type: input.DispatchMouseEventTypeMousePressed, X: x, Y: y, Button: input.MouseButtonLeft, Buttons: 1, ClickCount: 1},
		{Type: input.DispatchMouseEventTypeMouseReleased, X: x, Y: y, Button: input.MouseButtonLeft, ClickCount: 1},
	} {
		if _, err := chromedp.Call(ctx, input.DispatchMouseEvent, ev); err != nil {
			return nil, err
		}
	}
	b.settle(ctx, 5*time.Second)
	end()
	return b.snapshot(ctx)
}

const focusJS = `function(clear){
  const el=this;
  if(clear){
    if(typeof el.select==='function'){el.select();}
    else if(el.isContentEditable){const r=document.createRange();r.selectNodeContents(el);const s=getSelection();s.removeAllRanges();s.addRange(r);}
  } else if(typeof el.setSelectionRange==='function'){
    try{const n=el.value.length;el.setSelectionRange(n,n);}catch(e){}
  } else if(el.isContentEditable){const r=document.createRange();r.selectNodeContents(el);r.collapse(false);const s=getSelection();s.removeAllRanges();s.addRange(r);}
}`

const activeJS = `function(){
  let a=document.activeElement;
  while(a&&a.shadowRoot&&a.shadowRoot.activeElement)a=a.shadowRoot.activeElement;
  let inside=false;
  for(let n=a,i=0;n&&i<500;i++,n=n.parentNode||n.host){if(n===this){inside=true;break}}
  const g=n=>(a&&a.getAttribute&&a.getAttribute(n))||'';
  return {inside, tag:((a&&a.tagName)||'').toLowerCase(), type:((a&&typeof a.type==='string')?a.type:g('type')).toLowerCase(),
    autocomplete:g('autocomplete'), name:g('name'), id:(a&&a.id)||''};
}`

// checkFocus verifies the focused element is the ref's (or inside it) and is
// not a secret field. A ref'd element that has the focus but is not secret is
// fine; anything else is refused.
func (b *Browser) checkFocus(ctx context.Context, ref string, id cdp.BackendNodeID) error {
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: id})
	if err != nil || obj.Object == nil {
		return staleErr(ref)
	}
	res, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: activeJS, ObjectID: obj.Object.ObjectID, ReturnByValue: new(true),
	})
	if err != nil {
		return nodeGone(ref, err)
	}
	if res.ExceptionDetails != nil || res.Result == nil {
		return fmt.Errorf("could not check the focus for %s", ref)
	}
	var f struct {
		Inside                            bool
		Tag, Type, Autocomplete, Name, ID string
	}
	if err := json.Unmarshal(res.Result.Value, &f); err != nil {
		return err
	}
	if secretField(f.Tag, f.Type, f.Autocomplete, f.Name, f.ID) {
		return errors.New("werkz never types into password or payment fields (the focus is in one)")
	}
	if !f.Inside {
		return fmt.Errorf("focus moved to another field, so %s was not typed into: ReadPage again", ref)
	}
	return nil
}

// Type enters text into a field. Password and payment fields are refused,
// and so is typing when the focus is not on the field (a script moved it).
// pressEnter may submit a form: that needs allow.Submit.
func (b *Browser) Type(call context.Context, ref, text string, clear, pressEnter bool, allow Allow) (*Snapshot, error) {
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
	end, err := b.startAllow(ctx, allow)
	if err != nil {
		return nil, err
	}
	defer end()
	info, _ := b.lookup(ref)
	if _, err := chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: info.backend}); err != nil {
		return nil, nodeGone(ref, err)
	}
	if _, err := chromedp.Call(ctx, dom.Focus, dom.FocusParams{BackendNodeID: info.backend}); err != nil {
		return nil, nodeGone(ref, err)
	}
	obj, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: info.backend})
	if err != nil || obj.Object == nil {
		return nil, staleErr(ref)
	}
	if _, err := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: focusJS, ObjectID: obj.Object.ObjectID,
		Arguments: []*runtime.CallArgument{{Value: jsonv2.Value(strconv.FormatBool(clear))}},
	}); err != nil {
		return nil, err
	}
	if err := b.checkFocus(ctx, ref, info.backend); err != nil {
		return nil, err
	}
	if clear {
		if err := key(ctx, "Backspace", 8, ""); err != nil {
			return nil, err
		}
	}
	if text != "" {
		if _, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: text}); err != nil {
			return nil, err
		}
	}
	if pressEnter {
		if err := b.checkFocus(ctx, ref, info.backend); err != nil {
			return nil, fmt.Errorf("not pressing Enter: %w", err)
		}
		if err := key(ctx, "Enter", 13, "\r"); err != nil {
			return nil, err
		}
	}
	b.settle(ctx, 5*time.Second)
	end()
	return b.snapshot(ctx)
}

// key presses and releases one key; text is what it would type, if anything.
func key(ctx context.Context, name string, code int64, text string) error {
	down := input.DispatchKeyEventParams{
		Type: input.DispatchKeyEventTypeRawKeyDown, Key: name, Code: name,
		WindowsVirtualKeyCode: code, NativeVirtualKeyCode: code,
	}
	if text != "" {
		down.Type, down.Text, down.UnmodifiedText = input.DispatchKeyEventTypeKeyDown, text, text
	}
	up := input.DispatchKeyEventParams{
		Type: input.DispatchKeyEventTypeKeyUp, Key: name, Code: name,
		WindowsVirtualKeyCode: code, NativeVirtualKeyCode: code,
	}
	for _, ev := range []input.DispatchKeyEventParams{down, up} {
		if _, err := chromedp.Call(ctx, input.DispatchKeyEvent, ev); err != nil {
			return err
		}
	}
	return nil
}

// SetFiles attaches files to a file input (the page is not clicked).
func (b *Browser) SetFiles(call context.Context, ref string, paths []string) (*Snapshot, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	el, err := b.inspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !el.FileInput {
		return nil, fmt.Errorf("%s is not a file input", ref)
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		a, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		if st, err := os.Stat(a); err != nil || st.IsDir() {
			return nil, fmt.Errorf("%s is not a readable file", p)
		}
		abs[i] = a
	}
	info, _ := b.lookup(ref)
	if _, err := chromedp.Call(ctx, dom.SetFileInputFiles, dom.SetFileInputFilesParams{Files: abs, BackendNodeID: info.backend}); err != nil {
		return nil, nodeGone(ref, err)
	}
	b.settle(ctx, 5*time.Second)
	return b.snapshot(ctx)
}
