package browser

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
)

// HasSecretField reports whether the current page has a password, payment or
// one-time-code field (the fields secretField recognises, which Type refuses
// and Eval cannot guard completely), in the page itself, in iframes and in
// open or closed shadow roots. It reads the DOM through the protocol, not
// with a script of the page's world, so the page cannot make it miss a field.
// A field added after the call is not seen.
func (b *Browser) HasSecretField(call context.Context) (bool, error) {
	ctx, done, err := b.begin(call)
	if err != nil {
		return false, err
	}
	defer done()
	res, err := chromedp.Call(ctx, dom.GetDocument, dom.GetDocumentParams{Depth: new(int64(-1)), Pierce: new(true)})
	if err != nil {
		return false, err
	}
	return nodeHasSecret(res.Root), nil
}

func nodeHasSecret(n *cdp.Node) bool {
	if n == nil {
		return false
	}
	if n.NodeType == 1 {
		attrs := map[string]string{}
		for i := 0; i+1 < len(n.Attributes); i += 2 {
			attrs[strings.ToLower(n.Attributes[i])] = n.Attributes[i+1]
		}
		if secretField(strings.ToLower(n.NodeName), attrs["type"], attrs["autocomplete"], attrs["name"], attrs["id"]) {
			return true
		}
	}
	for _, kids := range [][]*cdp.Node{n.Children, n.ShadowRoots} {
		for _, c := range kids {
			if nodeHasSecret(c) {
				return true
			}
		}
	}
	return nodeHasSecret(n.ContentDocument) || nodeHasSecret(n.TemplateContent)
}
