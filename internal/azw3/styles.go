package azw3

import (
	"fmt"
	"regexp"

	"github.com/manzanit0/athenaeum/internal/kf8"
)

// Patterns for the parts of a stylesheet that need rewriting.
var (
	// cssURL matches a url() reference.
	cssURL = regexp.MustCompile(`url\(\s*['"]?([^'")]*)['"]?\s*\)`)
	// cssFontFace matches an embedded font declaration.
	cssFontFace = regexp.MustCompile(`(?s)@font-face\s*\{.*?\}`)
	// cssDirective matches the @import and @charset rules.
	cssDirective = regexp.MustCompile(`@(?:import|charset)[^;]*;`)
)

// styleURLs points the url() references in css, found in the file base,
// at the images of the book. References to anything else become none.
func (c *converter) styleURLs(css, base string) string {
	return cssURL.ReplaceAllStringFunc(css, func(match string) string {
		ref := cssURL.FindStringSubmatch(match)[1]
		if address, ok := c.imageAddress(ref, base); ok {
			return "url(" + address + ")"
		}
		return "none"
	})
}

// flow returns the number of the flow holding the stylesheet at name,
// adding it to the book the first time it is asked for.
func (c *converter) flow(name string) (int, bool) {
	if number, ok := c.flows[name]; ok {
		return number, number > 0
	}
	data, err := c.read(name)
	if err != nil {
		c.warn("stylesheet %s is missing", name)
		c.flows[name] = 0
		return 0, false
	}
	css := cssFontFace.ReplaceAllString(string(data), "")
	css = cssDirective.ReplaceAllString(css, "")
	c.book.Flows = append(c.book.Flows, []byte(c.styleURLs(css, name)))
	c.flows[name] = len(c.book.Flows)
	return len(c.book.Flows), true
}

// flowAddress returns the address of the flow with the given number.
func flowAddress(number int) string {
	return fmt.Sprintf("kindle:flow:%s?mime=text/css", kf8.Base32(number, 4))
}
