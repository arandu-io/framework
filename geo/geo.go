package geo

import (
	"context"
	stdhttp "net/http"

	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	hgeo "github.com/arandu-io/hesape/geo"
)

type (
	// Surface identifies one machine-facing HTTP representation.
	Surface = hgeo.Surface
	// Surfaces is a set of independently enabled GEO surfaces.
	Surfaces = hgeo.Surfaces
	// Config controls which native GEO surfaces exist and may expose public content.
	Config = hgeo.Config
	// Labels is the localized fixed vocabulary of model-facing documents.
	Labels = hgeo.Labels
	// RobotsPolicy is the application's public crawler policy.
	RobotsPolicy = hgeo.RobotsPolicy
	// Document is one public canonical page supplied by the application.
	Document = hgeo.Document
	// Catalog supplies application-authorized public documents.
	Catalog = hgeo.Catalog
	// CatalogFunc adapts a function into a Catalog.
	CatalogFunc = hgeo.CatalogFunc
)
type (
	// Metadata is the page state shared by SSR and enhanced navigation.
	Metadata = hgeo.Metadata
	// MetadataInput is the application-owned presentation of one page.
	MetadataInput = hgeo.MetadataInput
	// Alternate is one reciprocal language variant of a canonical page.
	Alternate = hgeo.Alternate
	// AlternateInput describes an alternate without repeating the deployment origin.
	AlternateInput = hgeo.AlternateInput
)

const (
	Robots   = hgeo.Robots
	Sitemap  = hgeo.Sitemap
	LLMs     = hgeo.LLMs
	LLMsFull = hgeo.LLMsFull

	SearchSurfaces     = hgeo.SearchSurfaces
	GenerativeSurfaces = hgeo.GenerativeSurfaces
	AllSurfaces        = hgeo.AllSurfaces
)

var (
	ErrInvalidConfig = hgeo.ErrInvalidConfig
	ErrCapacity      = hgeo.ErrCapacity
)

// MetadataFor creates canonical GEO metadata without reading request host headers.
func MetadataFor(config Config, input MetadataInput) (Metadata, error) {
	return hgeo.MetadataFor(config, input)
}

// Module translates the native GEO route descriptors into the Framework router.
type Module struct {
	inner *hgeo.Module
}

// NewModule returns the Framework envelope over the native GEO module.
func NewModule(config Config, catalog Catalog) *Module {
	return &Module{inner: hgeo.NewModule(config, catalog)}
}

var (
	_ kernel.Module   = (*Module)(nil)
	_ kernel.Bootable = (*Module)(nil)
)

// Name delegates the native module identifier.
func (m *Module) Name() string { return m.inner.Name() }

// Boot delegates native GEO configuration validation.
func (m *Module) Boot(ctx context.Context) error { return m.inner.Boot(ctx) }

// Routes registers the exact handlers selected by the native GEO module.
func (m *Module) Routes(router *http.Router) {
	for _, route := range m.inner.HTTPRoutes() {
		if route.Method != stdhttp.MethodGet {
			panic("geo: the Framework bridge only accepts native GET routes")
		}
		router.Get(route.Path, route.Handler.ServeHTTP).Name(route.Name)
	}
}
