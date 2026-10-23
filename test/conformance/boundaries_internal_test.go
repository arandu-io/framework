package conformance

import (
	"maps"
	"testing"
)

// cleanApplication is the smallest tree Boundaries reads as an application
// that keeps every boundary, with the near misses a careless check would
// flag: an event name outside app/Events, a method named like a copied type,
// an action string declared outside app/Policies, invalid Go in testdata and a
// view source that is not Go until it is built.
func cleanApplication() map[string]string {
	return map[string]string{
		"go.mod":                                "module example.test/app\n\ngo 1.26\n",
		"app/Http/Controllers/Auth/doc.go":      "// Package authui is where the sign-in screens are published.\npackage authui\n",
		"app/Models/User.go":                    "package models\n\ntype User struct{ ID string }\n\nfunc (User) TwoFactor() bool { return false }\n",
		"app/Mail/TwoFactorEnabled.go":          "package mail\n\ntype TwoFactorEnabled struct{}\n",
		"app/Http/Controllers/Actions.go":       "package controllers\n\nconst ActionUserView = \"page.view\"\n",
		"app/Policies/UserPolicy.go":            "package policies\n\nimport \"github.com/arandu-io/hesape/auth/account\"\n\nvar _ = account.ActionUserView\n",
		"bootstrap/app.go":                      "package bootstrap\n\nimport _ \"github.com/arandu-io/hesape/auth/account\"\n",
		"tests/Feature/testdata/broken.go":      "this is not Go\n",
		"app/Models/testdata/fixture.go":        "package fixture\n\ntype SessionGuard struct{}\n\ntype UserService struct{}\n",
		"app/Views/home.kyse.go":                "//go:build kyse\n\npackage views\n\n@extends('layouts.app')\n",
		"database/migrations/0001_users.go":     "package migrations\n",
		"routes/web.go":                         "package routes\n",
		"config/auth.go":                        "package config\n\n// Guard names the session guard the application signs in with.\nconst Guard = \"session\"\n",
		"tests/Conformance/Kits_test.go":        "package conformance_test\n",
		"vendor/example.test/x/SessionGuard.go": "package x\n\ntype SessionGuard struct{}\n",
	}
}

func with(files map[string]string, changes map[string]string) map[string]string {
	out := maps.Clone(files)
	for name, content := range changes {
		if content == "" {
			delete(out, name)
			continue
		}
		out[name] = content
	}
	return out
}

func TestBoundariesPassesAnApplicationThatKeepsThem(t *testing.T) {
	Boundaries(t, writeTree(t, cleanApplication()))
}

func init() {
	for name, c := range map[string]struct {
		changes map[string]string
		want    []string
	}{
		"Boundaries/community module imported": {
			map[string]string{"routes/web.go": "package routes\n\nimport _ \"github.com/arandu-io/framework/modules/auth\"\n"},
			[]string{"TheCommunityAuthModuleIsNotImported", "routes/web.go imports the community auth module"},
		},
		"Boundaries/placeholder in another package": {
			map[string]string{"app/Http/Controllers/Auth/doc.go": "package auth\n"},
			[]string{"TheAuthControllerPlaceholderIsPackageAuthui", "declares package auth, want authui"},
		},
		"Boundaries/placeholder missing": {
			map[string]string{"app/Http/Controllers/Auth/doc.go": ""},
			[]string{"parsing the auth controller placeholder"},
		},
		"Boundaries/SessionGuard named": {
			map[string]string{"config/auth.go": "package config\n\ntype SessionGuard struct{}\n"},
			[]string{"NoApplicationCodeNamesSessionGuard", "config/auth.go names the legacy SessionGuard"},
		},
		"Boundaries/copied file back": {
			map[string]string{"app/Services/TwoFactorService.go": "package services\n"},
			[]string{"TheAccountRulesAreNotCopied", "app/Services/TwoFactorService.go is back"},
		},
		"Boundaries/copied helper declared": {
			map[string]string{"app/Support/hash.go": "package support\n\nfunc HashRecoveryCode(string) string { return \"\" }\n"},
			[]string{"app/Support/hash.go declares HashRecoveryCode, which hesape/auth/account owns"},
		},
		"Boundaries/copied event declared": {
			map[string]string{"app/Events/Account.go": "package events\n\ntype TwoFactorEnabled struct{}\n"},
			[]string{"app/Events/Account.go declares TwoFactorEnabled"},
		},
		"Boundaries/user action redeclared": {
			map[string]string{"app/Policies/Actions.go": "package policies\n\nconst ActionUserNamesPublic = \"user.names.public\"\n"},
			[]string{"app/Policies/Actions.go declares ActionUserNamesPublic"},
		},
		"Boundaries/not an application": {
			map[string]string{"go.mod": ""},
			[]string{"is not an application root: it has no go.mod"},
		},
	} {
		broken[name] = brokenCase{
			run:  func(t *testing.T) { Boundaries(t, writeTree(t, with(cleanApplication(), c.changes))) },
			want: c.want,
		}
	}
}
