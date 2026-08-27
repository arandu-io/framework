package view

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"sort"
	"strings"

	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/validation"
)

// Layout is what a layout asks of the data every screen hands it.
//
// It lives here rather than in the application because every Arandu application
// declared the same ninety lines of it, and ninety lines nobody wrote are ninety
// lines nobody reads. A project that needs different chrome declares its own
// interface in the layout's @go block; this is the one the delivered layout uses.
//
// It is an interface rather than a struct so that pages with unrelated data
// share one frame: the layout asks for behaviour, and Page below is the
// implementation a page embeds to get it.
//
// # Why this one is declared and not aliased
//
// hesape/view.Layout asks for Any and All where this one asks for HasErrors
// and ErrorSummary. They are the same two questions under different names, and
// a layout is written against one spelling or the other -- so aliasing this
// name would ask every delivered layout to be rewritten, which is the opposite
// of what carrying the old name is for.
type Layout interface {
	// PageTitle is the document title, and what HTMX swaps on navigation.
	PageTitle() string
	// PageDescription is the meta description. Empty writes no tag, because a
	// missing description outranks an empty one.
	PageDescription() string
	// CanonicalURL is the absolute address of this page, or empty for none.
	CanonicalURL() string
	// IsCurrent says whether a navigation target is this page, so the header
	// can stop linking to where you already are.
	IsCurrent(href string) bool

	// BrandName is the application name in the navigation bar.
	BrandName() string
	// CSRFToken is what @csrf reads, and what <body> carries into every HTMX
	// request.
	CSRFToken() string

	// SignedIn decides which half of the navigation is drawn, and SignedInName
	// is who it greets.
	SignedIn() bool
	SignedInName() string

	// The navigation targets. An empty one draws no link, which is how a route
	// the application never registered stays out of the markup instead of
	// becoming a 404 the layout put there.
	HomeLink() string
	LoginLink() string
	LogoutLink() string
	RegisterLink() string

	// PanelLink is the signed-in person's own area, and AdminLink is the one
	// only some of them may open. Both follow the same rule as the four above:
	// empty draws nothing.
	//
	// AdminLink is empty for anybody who would be refused, which is what keeps
	// the header from ever offering an address that answers 403. The decision is
	// the controller's -- it holds the subject and the policy -- and the markup
	// only ever sees a string. A layout that asked about roles would be a second
	// place where authorization is decided, and the second place is the one that
	// gets it wrong.
	PanelLink() string
	AdminLink() string

	// HasErrors says whether the attempt that landed here was rejected, and
	// ErrorSummary is one line per failed field for the banner that says so.
	//
	// The banner is the layout's, which is why these two are here and the
	// per-field message is not: a page body draws "must be at least 12
	// characters" under the box it belongs to, and the layout draws the summary
	// once, above everything. FieldError and OldValue are promoted methods on
	// Page for the body to call, deliberately outside this interface -- Layout
	// is what the LAYOUT asks for.
	HasErrors() bool
	ErrorSummary() []string
}

// Page is the chrome every screen hands the layout, embedded rather than
// repeated:
//
//	type PostsIndexData struct {
//		view.Page
//		Posts []PostRow
//	}
//
// A page declares a struct of its own -- which is what turns a typo in a field
// name into a compile error -- and takes the frame from here.
//
// Nothing on it is a helper a view reaches for by itself. There is no config(),
// no route() and no auth(): the controller fills these in, so a name that drifts
// is a compile error rather than a blank link, and a form can never end up
// carrying another session's token under load.
//
// # Why this one is declared and not aliased
//
// The struct is already the same type as hesape/view.Page, field for field --
// Errors included, because Context and State are aliases here, so New fills
// this one from exactly what fills that one. What is not the same is the
// method set. The four error accessors were renamed on the way over --
// FieldError to First, FieldErrors to Get, HasErrors to Any, ErrorSummary to
// All -- and the component library calls FieldError from a module this one
// does not compile, so an alias would build here and leave every field
// component with no method to call. It would also drop LogValue and
// MarshalJSON, which are declared here and have no counterpart there.
type Page struct {
	// Title is the document title.
	Title string
	// Description is the meta description, and the og:description. Leave it
	// empty on a page that has nothing specific to say.
	Description string
	// Canonical is the absolute URL of this page. It is what stops the same post
	// counting twice when it answers on more than one address.
	Canonical string

	// AppName is the brand in the navigation bar.
	AppName string

	// Token is the CSRF token issued for this session. It reaches the markup
	// twice: as the hidden field @csrf writes, and as the hx-headers attribute
	// on <body> that makes every HTMX request carry it.
	Token string

	// Authenticated decides which half of the navigation bar is drawn, and
	// UserName is the signed-in person's display name.
	Authenticated bool
	UserName      string

	// Where the navigation points. They come from the router, through the
	// controller. RegisterURL is empty when registration is not open.
	HomeURL     string
	LoginURL    string
	LogoutURL   string
	RegisterURL string

	// PanelURL and AdminURL are drawn only when signed in, and AdminURL only
	// for somebody the policy would let in. Filled by the controller, from the
	// subject it already holds -- see the Layout interface.
	PanelURL string
	AdminURL string

	// Path is the address this page was served at, so the navigation can say
	// where you are.
	//
	// A header that offers "Sign in" on the sign-in page is a header with a link
	// to the page you are reading -- and the one control that would help, the
	// way out to registering, is the one it does not show. The layout reads this
	// through IsCurrent; nothing else needs it.
	Path string

	// Errors is what validation rejected on the attempt that was sent back
	// here, keyed by the name of the form input -- the same name
	// components.FieldProps.Name carries.
	//
	// It is filled by New, from the flash, and by nothing else. No handler
	// assigns it: a handler that had to would be a handler that can forget to,
	// and forgetting is invisible -- the form comes back with no messages on it,
	// which is the failure this field exists to end.
	//
	// Empty on every page nobody was rejected on, which is nearly all of them.
	Errors validation.Errors

	// Old is what was typed on that attempt, so the boxes come back filled in
	// rather than blank.
	//
	// It never carries a password. See security.Flash for the list and for why
	// the MESSAGE for a password survives when the value does not: an empty
	// password box that does not say why it was rejected is the original bug.
	Old url.Values
}

// Compile-time proof that embedding Page is all a page has to do to fit the
// layout. If the layout asks for something else, this line is where the build
// stops -- in one file, naming the contract, rather than in every page at once.
//
// The other two are the ones that keep the token off the debug page. They are
// asserted here because deleting either method is otherwise a silent change:
// nothing stops compiling, and the next dump publishes the token.
var (
	_ Layout         = Page{}
	_ json.Marshaler = Page{}
	_ slog.LogValuer = Page{}
)

// redacted is what a secret looks like once it has left this package.
//
// The value never appears. Whether there was one does: an empty string stays
// empty, and anything else becomes the marker. A secret that simply vanished
// from the output would read exactly like a field nobody filled in, and "the
// form posted an empty token" is the failure a page is dumped to find.
func redacted(secret string) string {
	if secret == "" {
		return ""
	}
	return "[redacted]"
}

// MarshalJSON keeps the CSRF token out of anything that serializes a page.
// Without it a single dump of the data a screen renders from publishes a token
// that is live for that session, on a page whose whole purpose is to be read.
//
// It names the fields that may leave, rather than the ones that may not. A
// field added to the struct later does not appear until it is named here, which
// is the direction that cannot leak by accident -- the reverse spelling grows a
// hole every time somebody adds a field and does not think about this method.
//
// Old and Errors are carried whole. Neither is a secret: the values a form
// posts back are already drawn into the boxes the browser shows, and the
// messages carry the limit a rule declared and never what was typed against it.
//
// A struct that embeds Page and adds fields of its own inherits this method,
// and inheriting it means those fields are not serialized -- the token is
// redacted and the rest of the screen's data does not appear. A page that is
// dumped for its own data declares its own MarshalJSON, naming what it carries
// alongside the chrome named here.
func (p Page) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title       string
		Description string
		Canonical   string
		AppName     string
		Token       string

		Authenticated bool
		UserName      string

		HomeURL     string
		LoginURL    string
		LogoutURL   string
		RegisterURL string
		PanelURL    string
		AdminURL    string

		Path string

		Errors validation.Errors
		Old    url.Values
	}{
		Title:       p.Title,
		Description: p.Description,
		Canonical:   p.Canonical,
		AppName:     p.AppName,
		Token:       redacted(p.Token),

		Authenticated: p.Authenticated,
		UserName:      p.UserName,

		HomeURL:     p.HomeURL,
		LoginURL:    p.LoginURL,
		LogoutURL:   p.LogoutURL,
		RegisterURL: p.RegisterURL,
		PanelURL:    p.PanelURL,
		AdminURL:    p.AdminURL,

		Path: p.Path,

		Errors: p.Errors,
		Old:    p.Old,
	})
}

// LogValue implements [slog.LogValuer], so a log line handed a whole page
// records which screen it was and nothing else.
//
// Shorter than MarshalJSON on purpose, and the token is not among the three.
// A log line is shipped to an aggregator and kept, and neither the display name
// of the person reading the screen nor the address they typed into a form is
// something to keep there; a dump is one request, on one machine, in
// development.
func (p Page) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", p.Path),
		slog.String("title", p.Title),
		slog.Bool("authenticated", p.Authenticated),
	)
}

// IsCurrent reports whether a navigation target is the page being read.
//
// It compares paths and ignores the query, because "?resent=1" is still the same
// page -- a navigation that changes when a flash message is added is one nobody
// can predict.
//
// An empty href is never current: an empty URL draws no control, and a control
// that is not drawn cannot be the one you are on.
func (p Page) IsCurrent(href string) bool {
	if href == "" || p.Path == "" {
		return false
	}
	if at := strings.IndexByte(href, '?'); at >= 0 {
		href = href[:at]
	}
	return strings.TrimSuffix(p.Path, "/") == strings.TrimSuffix(href, "/")
}

// PageTitle is what the browser tab shows.
func (p Page) PageTitle() string { return p.Title }

// PageDescription is the meta description of this page.
func (p Page) PageDescription() string { return p.Description }

// CanonicalURL is the address search engines should treat as this page's own.
func (p Page) CanonicalURL() string { return p.Canonical }

// BrandName is the application name, shown in the navigation bar.
func (p Page) BrandName() string { return p.AppName }

// CSRFToken is what @csrf reads to write the hidden field.
//
// It is a method rather than the field itself because the field is also
// interpolated into hx-headers, and one name cannot be both.
func (p Page) CSRFToken() string { return p.Token }

// SignedIn reports whether there is a session behind this render.
func (p Page) SignedIn() bool { return p.Authenticated }

// SignedInName is who the navigation bar greets.
func (p Page) SignedInName() string { return p.UserName }

// HomeLink is where the brand points.
func (p Page) HomeLink() string { return p.HomeURL }

// LoginLink is the sign-in screen.
func (p Page) LoginLink() string { return p.LoginURL }

// LogoutLink is what the sign-out form posts to.
func (p Page) LogoutLink() string { return p.LogoutURL }

// RegisterLink is the sign-up screen, or empty when registration is closed.
func (p Page) RegisterLink() string { return p.RegisterURL }

// PanelLink is the signed-in person's own area, or empty.
func (p Page) PanelLink() string { return p.PanelURL }

// AdminLink is the administration area, or empty for anybody who would be
// refused it.
func (p Page) AdminLink() string { return p.AdminURL }

// New returns the page chrome for this request, with the messages and the typed
// input of a rejected attempt already on it.
//
//	Page: view.New(ctx, "New post").WithToken(token),
//
// It replaces the view.Page{Title: ..., Token: ...} literal a controller used to
// write, and the difference is the whole point of this file: nothing in that
// line mentions errors, and the errors are on the page. There is no argument to
// pass and therefore none to forget, which matters because forgetting produces a
// form that comes back blank -- correct-looking, and wrong.
//
// It fills only what the request itself knows: the title it was given, the
// address being served, and what the flash left behind. The application name,
// the navigation and the signed-in person are the controller's, because they are
// decisions -- see the Layout interface on why the layout is never allowed to go
// and fetch them.
func New(ctx *http.Context, title string) Page {
	state := ctx.State()
	return Page{
		Title:  title,
		Path:   ctx.Request.URL.Path,
		Errors: state.Errors,
		Old:    state.Old,
	}
}

// WithToken sets the CSRF token, which the controller issues.
//
// A method rather than a field in the literal, so that New reads as one
// expression at the call site. It returns a copy: Page is a value everywhere
// else, and a builder that mutated in place would be the one method on it that
// does.
func (p Page) WithToken(token string) Page {
	p.Token = token
	return p
}

// FieldError is the first message for a field, or empty.
//
// One message and not all of them, because that is what a form draws: the box
// has room for one line, and the first is the one that names what to change.
// It is what components.FieldProps.Error takes:
//
//	Error: .FieldError("email")
//
// Empty for a field that was accepted, and empty for every field on a page
// nobody was rejected on -- so a screen asks unconditionally and draws nothing
// when there is nothing. There is no @error directive and none is needed.
func (p Page) FieldError(name string) string {
	msgs := p.Errors[name]
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}

// FieldErrors is every message for a field, for the rare screen that lists them.
func (p Page) FieldErrors(name string) []string { return p.Errors[name] }

// HasErrors reports whether the attempt that landed here was rejected.
//
// It is what a layout asks before drawing the banner. A page with no errors must
// not draw an empty one: a box that is always there and usually blank is a box
// people stop reading, which is how a real message goes unseen.
func (p Page) HasErrors() bool { return len(p.Errors) > 0 }

// OldValue is what was typed in a field on the attempt that was rejected.
//
//	Value: .OldValue("email")
//
// Always empty for a password, by construction rather than by the screen
// remembering to leave it out. See security.Flash.
func (p Page) OldValue(name string) string { return p.Old.Get(name) }

// OldOr is OldValue, falling back to what the field already held.
//
// It is what an edit form needs and a create form does not: the box starts at
// the stored value, and comes back carrying the rejected edit rather than
// reverting to what is in the database -- which would quietly undo the change
// somebody is in the middle of making.
//
// It answers the fallback when nothing was flashed, and the flashed value even
// when that value is empty: a field somebody deliberately cleared must not fill
// itself back in.
func (p Page) OldOr(name, fallback string) string {
	if _, typed := p.Old[name]; typed {
		return p.Old.Get(name)
	}
	return fallback
}

// ErrorSummary is one line per failed field, for the banner, in sorted field
// order.
//
//	Password must be at least 12 characters
//
// The field name is prepended HERE and not baked into the message. A message
// written as ":attribute must be at least :min characters" carries the name
// everywhere, including under the labelled box where the label has just said it.
// So a message reads bare where it is drawn in context, and named where it is
// drawn out of context, and there is one message either way.
//
// Sorted rather than in map order, so the banner does not reshuffle between two
// renders of the same failure.
func (p Page) ErrorSummary() []string {
	if len(p.Errors) == 0 {
		return nil
	}

	fields := make([]string, 0, len(p.Errors))
	for field := range p.Errors {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		for _, msg := range p.Errors[field] {
			// validation.Humanize and not a copy of it here: the same field name
			// is turned into the same words by the package that writes the
			// messages, so the banner and anything else that needs a field's
			// name cannot disagree on the first line somebody reads.
			out = append(out, validation.Humanize(field)+" "+msg)
		}
	}
	return out
}
