// Package storage is the file contract: what a tenant uploaded, and where it
// went.
//
// It is not an optional package: a file is customer data, and a path without a
// tenant is a leak with a directory name. Every operation takes a
// security.Grant, and the stored path is prefixed by the tenant that Grant
// carries.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/filesystem directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. One hesape package
// answers for all of it:
//
//	hesape/filesystem  Key, CleanKey, ErrNotFound, ErrNoTenant, ErrBadKey,
//	                   and the Adapter/Disk pair that replaced Store
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: the errors are Go aliases, Path
// and CleanKey are one-line calls through, and the two shapes that could not
// follow the rename are declared with their old form and nothing else.
//
// # What the rename reshaped
//
// hesape split the old Store in two. An Adapter is what a driver implements and
// it never hears of a tenant; a Disk is what an application calls and every one
// of its methods takes a Grant; between them sits filesystem.Key, which turns a
// Grant and a key into the one stored path that Grant may reach. Path was
// renamed to Key in the move, and that split is why: the prefix is applied once,
// in hesape, instead of in each driver remembering to ask.
//
// # The two shapes that stay declared here, and why
//
//	Store  hesape/filesystem.Adapter takes stored paths and no Grant, and has a
//	       sixth method (Stat). A driver implements the five-method,
//	       Grant-taking shape from a module this one does not compile, so an
//	       alias would compile here and break it in silence.
//	File   hesape/filesystem.File carries its metadata in an embedded Info, so
//	       the flat composite literal a driver writes does not compile against
//	       it. The fields and their meanings are unchanged.
//
// Neither declaration is a way around the Grant: the only thing that produces a
// stored path is Path, and Path needs one.
package storage
