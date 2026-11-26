package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	findMax     = 20
	maxPageText = 100000
	maxEval     = 20000
	evalTimeout = 10 * time.Second
)

// Find searches the accessibility tree and returns the matching lines as
// ReadPage prints them ("[ref_12] button "Save""), at most limit (default and
// maximum 20), in page order. The query matches, case-insensitively, a node's
// name, value or description, or its role ("button", "link", "textbox",
// "heading" ...); "button save" means a button whose text has "save". Lines
// with a ref come before lines without one (plain text). The tree is read
// afresh and keeps ReadPage's refs for nodes seen before. The value of a
// password or payment field is never shown or searched.
func (b *Browser) Find(call context.Context, query string, limit int) ([]string, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, errors.New("query is empty")
	}
	if limit <= 0 || limit > findMax {
		limit = findMax
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return nil, err
	}
	defer done()
	lines, err := b.buildTree(ctx, false, false)
	if err != nil {
		return nil, err
	}
	role, text := "", q
	if first, rest, ok := strings.Cut(q, " "); ok && knownRole(first) {
		role, text = first, strings.TrimSpace(rest)
	} else if knownRole(q) {
		role, text = q, ""
	}
	textMatch := func(l treeLine, t string) bool {
		if t == "" {
			return false
		}
		for _, f := range []string{l.name, l.value, l.desc} {
			if strings.Contains(strings.ToLower(f), t) {
				return true
			}
		}
		return false
	}
	matches := func(l treeLine) bool {
		switch {
		case role != "" && text != "":
			return l.role == role && textMatch(l, text)
		case role != "":
			return l.role == role || textMatch(l, role) // "button" alone also finds the word in names
		}
		return textMatch(l, text)
	}
	var withRef, without []string
	for _, l := range lines {
		if !matches(l) {
			continue
		}
		s := strings.TrimLeft(l.text, " ")
		if l.hasRef {
			withRef = append(withRef, s)
		} else {
			without = append(without, s)
		}
	}
	out := append(withRef, without...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func knownRole(s string) bool {
	switch s {
	case "heading", "image", "img", "text", "statictext":
		return true
	}
	return interactiveRoles[s]
}

const pageTextJS = `(function(){
  var root=document.querySelector('main')||document.querySelector('article')||document.querySelector('[role=main]')||document.body||document.documentElement;
  var secrets=[];
  var f=document.querySelectorAll('input,textarea');
  for(var i=0;i<f.length;i++){var e=f[i];
    var ac=String(e.getAttribute('autocomplete')||'').toLowerCase();
    var nm=String(e.getAttribute('name')||'')+' '+String(e.id||'');
    if(String(e.type||'').toLowerCase()==='password'||/current-password|new-password|one-time-code|cc-number|cc-csc|cc-exp/.test(ac)||/card[-_ ]?(number|num|no)\b|\bcvc\b|\bcvv\b|cc[-_]?(num|number|csc|exp)/i.test(nm)){
      var v=e.value;if(v&&v.length>=2)secrets.push(v);}}
  var t=root?root.innerText:'';
  for(var j=0;j<secrets.length;j++){t=t.split(secrets[j]).join('[secret]')}
  return {title:document.title,text:t};
})()`

var (
	spaceRun = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankRun = regexp.MustCompile(`\n{3,}`)
)

// PageText returns the page title and its readable text: the <main>,
// <article> or [role=main] element when there is one (so navigation and
// footers stay out), else the body. It is innerText, so only what is visible
// counts; whitespace is collapsed and the text is capped at 100,000
// characters. Values of password and payment fields are never part of it.
func (b *Browser) PageText(call context.Context) (title, text string, err error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return "", "", err
	}
	defer done()
	r, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: pageTextJS, ReturnByValue: new(true)})
	if err != nil {
		return "", "", err
	}
	if r.ExceptionDetails != nil || r.Result == nil {
		return "", "", errors.New("could not read the page text")
	}
	var out struct{ Title, Text string }
	if err := json.Unmarshal(r.Result.Value, &out); err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.ReplaceAll(out.Text, "\r", ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRun.ReplaceAllString(l, " "))
	}
	text = strings.TrimSpace(blankRun.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	if r := []rune(text); len(r) > maxPageText {
		text = string(r[:maxPageText]) + "\n… (cut at 100,000 characters)"
	}
	return strings.TrimSpace(out.Title), text, nil
}

// Eval runs JavaScript in the page and returns the result: a string as is,
// anything else as JSON ("undefined" for no value), capped at 20,000
// characters. The script runs as in the DevTools console (top-level await and
// repeated let/const work), may return a promise (awaited), and is cut off
// after 10 seconds. A thrown exception comes back as an error with its
// message.
//
// Secret fields: while the script runs, reading or writing .value,
// .defaultValue or .valueAsNumber of a password or payment input (the same
// fields Type refuses) throws, and form submissions and downloads stay
// blocked. The limits are real: this overrides the accessors on the
// prototypes, which stops accidental or naive reads, but it is not a sandbox.
// A script can still reach a field's text in other ways (FormData, outerHTML
// of a prefilled value attribute, a fresh iframe's own prototypes, or
// handlers of the page that run for it), and it is no protection against a
// script that is written to get around it. Same-origin iframes are covered,
// cross-origin ones are not.
func (b *Browser) Eval(call context.Context, js string) (string, error) {
	if strings.TrimSpace(js) == "" {
		return "", errors.New("script is empty")
	}
	ctx, done, err := b.begin(call)
	if err != nil {
		return "", err
	}
	defer done()
	sw := func(c context.Context, on string) {
		_, _ = chromedp.Call(c, runtime.Evaluate, runtime.EvaluateParams{Expression: fmt.Sprintf(evalSwitchJS, on, b.guardKey)})
	}
	sw(ctx, "true")
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		sw(c, "false")
	}()
	ectx, cancel := context.WithTimeout(ctx, evalTimeout)
	defer cancel()
	res, err := chromedp.Call(ectx, runtime.Evaluate, runtime.EvaluateParams{
		Expression: js, ReplMode: new(true),
		Timeout: runtime.TimeDelta(evalTimeout.Milliseconds()),
	})
	var details *runtime.ExceptionDetails
	var obj *runtime.RemoteObject
	if err == nil {
		details, obj = res.ExceptionDetails, res.Result
		if details == nil && obj != nil && obj.Subtype == runtime.RemoteObjectSubtypePromise {
			// REPL mode does not await a promise result by itself
			var ar runtime.AwaitPromiseResult
			ar, err = chromedp.Call(ectx, runtime.AwaitPromise, runtime.AwaitPromiseParams{PromiseObjectID: obj.ObjectID, ReturnByValue: new(true)})
			if err == nil {
				details, obj = ar.ExceptionDetails, ar.Result
			}
		} else if details == nil && obj != nil && obj.ObjectID != "" {
			cr, cerr := chromedp.Call(ectx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
				FunctionDeclaration: "function(){return this}", ObjectID: obj.ObjectID, ReturnByValue: new(true),
			})
			if cerr == nil && cr.ExceptionDetails == nil && cr.Result != nil {
				obj = cr.Result // by value; otherwise keep the description
			}
		}
	}
	if err != nil {
		if ectx.Err() != nil && call.Err() == nil {
			tctx, tcancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_, _ = chromedp.Call(tctx, runtime.TerminateExecution, cdp.Empty{})
			tcancel()
			return "", errors.New("the script did not finish within 10s")
		}
		return "", err
	}
	res = runtime.EvaluateResult{Result: obj, ExceptionDetails: details}
	if d := res.ExceptionDetails; d != nil {
		msg := d.Text
		if d.Exception != nil {
			switch {
			case d.Exception.Description != "":
				msg = d.Exception.Description
			case len(d.Exception.Value) > 0:
				msg = string(d.Exception.Value)
			}
		}
		return "", errors.New(strings.TrimSpace(msg))
	}
	if res.Result == nil {
		return "undefined", nil
	}
	out := res.Result.Description
	switch {
	case res.Result.Type == runtime.RemoteObjectTypeUndefined:
		out = "undefined"
	case string(res.Result.Value) == "null":
		out = "null"
	case len(res.Result.Value) > 0:
		var s string
		if json.Unmarshal(res.Result.Value, &s) == nil {
			out = s
		} else {
			out = string(res.Result.Value)
		}
	case res.Result.UnserializableValue != "":
		out = string(res.Result.UnserializableValue)
	case out == "":
		out = string(res.Result.Type)
	}
	if r := []rune(out); len(r) > maxEval {
		out = string(r[:maxEval]) + "… (cut at 20,000 characters)"
	}
	return out, nil
}
