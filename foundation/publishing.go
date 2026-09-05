// The publishing vocabulary, answered by github.com/arandu-io/hesape/foundation.
//
// Everything here is an alias or a call through. A module that declares
// Publishes() against either name is one type to the compiler, and the closed
// set of tags is enforced in one place rather than restated here.

package foundation

import hfoundation "github.com/arandu-io/hesape/foundation"

// PublishTag names the kind of file a module publishes.
//
// The set is closed at six. A tag added on demand turns publishing into a
// plugin surface, where every module teaches the command a new word and nobody
// can say what publishing does without reading every module. What does not fit
// one of the six is written in Go rather than published.
//
// A tag says what a file is, never where it goes: the destination is on the
// publication, because a module publishes into the directories a project
// already has and those are not one per kind.
type PublishTag = hfoundation.PublishTag

const (
	// PublishView is a page or a layout the project is meant to edit.
	PublishView = hfoundation.PublishView
	// PublishComponent is a piece a view composes.
	PublishComponent = hfoundation.PublishComponent
	// PublishConfig is configuration the project owns once it is published.
	PublishConfig = hfoundation.PublishConfig
	// PublishMigration is a schema change the project applies and keeps.
	PublishMigration = hfoundation.PublishMigration
	// PublishTranslation is a catalogue of sentences.
	PublishTranslation = hfoundation.PublishTranslation
	// PublishAsset is a file served as it is: an image, a font, a stylesheet.
	PublishAsset = hfoundation.PublishAsset
)

// PublishTags lists the six, in the order a listing prints them.
//
// A wrapper and not an alias, because Go has no alias form for a function. It
// answers the same slice the hesape function does, so a command that prints the
// set and a module that declares a tag are reading one list.
func PublishTags() []PublishTag { return hfoundation.PublishTags() }

// Publication is one tree of files a module publishes under one tag.
//
// It is data and not behaviour: the module says what it has and where it goes,
// and the engine that writes it lives elsewhere, so a module that publishes
// does not drag file IO into every binary that merely compiles it.
type Publication = hfoundation.Publication

// Publishable is optional: the module declares the files it offers a project.
//
// It is the same category as [Migratable] -- a capability a module claims about
// itself -- and it is asked for the same way, from the Application, when
// something wants to write those files into a project.
type Publishable = hfoundation.Publishable

// Publications returns what one module publishes, and nothing when it publishes
// nothing.
//
// A wrapper for the same reason PublishTags is. It is where the closed set is
// enforced, so a tag from outside the six is an error here rather than a
// directory nobody expected.
//
// [Application.Publications] is the collection over every registered module;
// this one answers about a single value.
func Publications(module any) ([]Publication, error) {
	return hfoundation.Publications(module)
}
