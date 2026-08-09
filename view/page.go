package view

import "strings"

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

	// Path is the address this page was served at, so the navigation can say
	// where you are.
	//
	// A header that offers "Sign in" on the sign-in page is a header with a link
	// to the page you are reading -- and the one control that would help, the
	// way out to registering, is the one it does not show. The layout reads this
	// through IsCurrent; nothing else needs it.
	Path string
}

// Compile-time proof that embedding Page is all a page has to do to fit the
// layout. If the layout asks for something else, this line is where the build
// stops -- in one file, naming the contract, rather than in every page at once.
var _ Layout = Page{}

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
