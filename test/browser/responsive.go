package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Widths are the viewport widths Responsive walks, from a desktop down to the
// narrowest phone still sold.
var Widths = []int{1440, 768, 390, 320}

// Schemes are the colour schemes Responsive walks.
var Schemes = []string{"light", "dark"}

// heights pairs each width with the height of a device that has it, so a
// screenshot looks like the device and not like a strip.
var heights = map[int]int{1440: 900, 768: 1024, 390: 844, 320: 640}

// Responsive walks the page through every width in Widths in every scheme in
// Schemes, and fails the test when the page is wider than the viewport in any
// of them. Each combination is a step in the trace with its screenshot; an
// overflowing one also carries the geometry of what sticks out -- each
// element past the right edge, its box, its computed width rules and the
// ancestors that clip it -- because "it overflows at 320" is where the
// question starts, not where it ends.
//
// The scheme is emulated with prefers-color-scheme, and the dark class on the
// root element is set to match, for pages whose stylesheet keys dark mode on
// the class. The viewport and scheme the page had are restored afterwards.
func (p *Page) Responsive() {
	p.t.Helper()
	var overflowed []string
	for _, width := range Widths {
		for _, scheme := range Schemes {
			combination := fmt.Sprintf("%d %s", width, scheme)
			var geometry json.RawMessage
			p.stepWith("responsive", combination, func(ctx context.Context) (json.RawMessage, error) {
				v := Viewport{Width: width, Height: heights[width]}
				if v.Height == 0 {
					v.Height = 900
				}
				if err := p.emulate(ctx, v, scheme); err != nil {
					return nil, err
				}
				if err := p.call(ctx, "overflow()", &geometry); err != nil {
					return nil, err
				}
				if string(geometry) == "null" {
					geometry = nil
				}
				return geometry, nil
			})
			if geometry != nil {
				overflowed = append(overflowed, combination)
			}
		}
	}
	p.step("responsive restore", "", func(ctx context.Context) error { return p.emulate(ctx, p.viewport, "light") })
	if len(overflowed) > 0 {
		p.t.Fatalf("browser: the page is wider than the viewport at %s; the trace has the geometry of what sticks out",
			strings.Join(overflowed, ", "))
	}
}

// emulate sets the viewport and the colour scheme and waits for the page to
// settle into them.
func (p *Page) emulate(ctx context.Context, v Viewport, scheme string) error {
	if err := p.d.SetViewport(ctx, v); err != nil {
		return err
	}
	if err := p.d.EmulateMedia(ctx, Media{ColorScheme: scheme, ReducedMotion: "no-preference"}); err != nil {
		return err
	}
	dark, _ := json.Marshal(scheme == "dark")
	if err := p.d.Evaluate(ctx, "document.documentElement.classList.toggle('dark', "+string(dark)+")", nil); err != nil {
		return err
	}
	return p.settle(ctx)
}

// stepWith is step for a step that measured something worth keeping in the
// trace as it came.
func (p *Page) stepWith(action, target string, fn func(ctx context.Context) (json.RawMessage, error)) {
	p.t.Helper()
	var data json.RawMessage
	p.stepData(action, target, &data, func(ctx context.Context) error {
		var err error
		data, err = fn(ctx)
		return err
	})
}
