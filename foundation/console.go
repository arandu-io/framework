package foundation

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/console"
	hfoundation "github.com/arandu-io/hesape/foundation"
)

// Commandable is optional: the module declares the console commands it adds to
// the application's binary. The kernel collects them from every registered
// module in registration order, beside the commands it composes itself and the
// application's own, and a name that is already taken stops the composition
// naming who declared each.
type Commandable = hfoundation.Commandable

// isolationLockTTL is how long an isolated command may hold its lock.
//
// It is the deadlock protection and nothing else: a process that dies partway
// through holds the lock until it expires. So it is sized above the longest
// migration rather than against the usual one -- an hour of refused runs after
// a crash is the price of a lock that cannot expire under a migrator still
// using it, and the other failure is two migrators altering one table.
const isolationLockTTL = time.Hour

// declared is one command the kernel answers, with who declared it and whether
// the application is booted before it runs.
type declared struct {
	command console.Command
	// by names the declarer in the refusal of a repeated name: the framework,
	// the application or a module.
	by string
	// boots is true for a command that reaches what Boot wires -- the routes,
	// the job handlers, the scheduler, the application's own services -- and
	// false for one that reads the configuration and the database alone.
	boots bool
}

// consoleKernel is what HandleCommand dispatches against: the commands in
// declaration order, the names that forward to another, and what an isolated
// command takes its lock from.
type consoleKernel struct {
	commands []declared
	aliases  map[string]string

	environment config.Env
	// locks is the issuer every replica sees, or nil when every store is kept
	// inside this process.
	locks *cache.Locks
	// close releases what the composition opened, once the command is done.
	close func() error
}

// lookup answers the command a name stands for, following an alias.
func (k *consoleKernel) lookup(name string) (declared, bool) {
	if target, ok := k.aliases[name]; ok {
		name = target
	}
	for _, d := range k.commands {
		if d.command.Name == name {
			return d, true
		}
	}
	return declared{}, false
}

// named answers the command with its Name filled in the way the console
// registry fills it: a command that declares a Signature and leaves Name empty
// is named by the first word of the signature, and a Name that is set wins. A
// signature that does not parse is the error.
func named(c console.Command) (console.Command, error) {
	name, _, _, err := c.Definition()
	if err != nil {
		return c, err
	}
	if strings.TrimSpace(c.Name) == "" {
		c.Name = name
	}
	return c, nil
}

// compose checks that every name is declared once, and answers the kernel or
// the refusal naming both declarers of the first repeated name.
//
// Every command is named first, so a command declared by its Signature alone
// is listed, looked up, refused and run under the name its signature gives.
//
// A repeated name is refused rather than resolved by order, because a module
// or an application that could shadow `migrate` could change what a deploy
// step does, and a built-in that silently lost to one would be worse.
func compose(commands []declared, aliases map[string]string) (*consoleKernel, error) {
	seen := map[string]string{}
	for alias, target := range aliases {
		seen[alias] = "the framework (as another name for " + target + ")"
	}
	commands = slices.Clone(commands)
	for i := range commands {
		d := &commands[i]
		command, err := named(d.command)
		if err != nil {
			return nil, fmt.Errorf("arandu: %s declared a console command whose signature %q does not parse: %w",
				d.by, d.command.Signature, err)
		}
		d.command = command
		name := command.Name
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("arandu: %s declared a console command with no name and no signature", d.by)
		}
		if d.command.Run == nil {
			return nil, fmt.Errorf("arandu: %s declared the console command %q with no Run", d.by, name)
		}
		if first, taken := seen[name]; taken {
			return nil, fmt.Errorf("arandu: the console command %q is declared by %s and again by %s; "+
				"a command name is answered by one declarer, so rename the second one", name, first, d.by)
		}
		seen[name] = d.by
	}
	return &consoleKernel{commands: commands, aliases: aliases}, nil
}

// WithConsole sets the streams HandleCommand writes to and reads from. They
// are os.Stdout, os.Stderr and os.Stdin until it is called; a test passes
// buffers.
func (a *Application) WithConsole(out, errOut io.Writer, in io.Reader) *Application {
	a.consoleOut, a.consoleErr, a.consoleIn = out, errOut, in
	return a
}

// HandleCommand runs one command line against the application and answers the
// status the process exits with: the name, then its arguments. No arguments is
// serve, so `go run .` -- the first thing anybody types after a clone -- starts
// the server.
//
// It is the whole of an application's main:
//
//	app, err := bootstrap.App()
//	if err != nil {
//		fmt.Fprintln(os.Stderr, err)
//		os.Exit(1)
//	}
//	os.Exit(app.HandleCommand(context.Background(), os.Args[1:]))
//
// The commands are the closed set the framework composes, the application's
// own and those of every Commandable module, from one list written when the
// application was created; help, list, -h and --help print it. A name that is
// not in it prints the list and answers 1.
//
// A command that reaches what Boot wires boots the application first and shuts
// it down after; one that reads only the configuration and the database does
// neither, so `migrate` opens no socket and starts no scheduler. Whatever the
// composition opened is closed before HandleCommand returns.
//
// An isolated command -- the migrations, the queue's prunes -- takes its lock
// from the store every replica sees. Without one it is refused outside
// development rather than reporting itself isolated while N replicas run it.
func (a *Application) HandleCommand(ctx context.Context, args []string) int {
	out, errOut, in := a.consoleStreams()

	k := a.kernel
	if k == nil {
		var err error
		if k, err = a.applicationKernel(); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if k.close != nil {
		defer func() { _ = k.close() }()
	}

	name, rest := "serve", []string(nil)
	if len(args) > 0 {
		name, rest = args[0], args[1:]
	}
	switch name {
	case "help", "list", "-h", "--help":
		fmt.Fprint(out, k.help())
		return 0
	}

	d, found := k.lookup(name)
	if !found {
		fmt.Fprintf(errOut, "unknown command: %s\n\n%s", name, k.help())
		return 1
	}
	if err := k.refuse(d.command); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if d.boots {
		if err := a.Boot(ctx); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		defer func() { _ = a.Shutdown() }()
	}

	err := k.registry(out, errOut, in).Call(ctx, d.command.Name, rest...)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return console.ExitCode(err)
	}
	return 0
}

// consoleStreams answers the streams WithConsole set, or the process's own.
func (a *Application) consoleStreams() (io.Writer, io.Writer, io.Reader) {
	out, errOut, in := a.consoleOut, a.consoleErr, a.consoleIn
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	if in == nil {
		in = os.Stdin
	}
	return out, errOut, in
}

// registry is the console registry the command runs in: every command, so a
// command that calls another finds it, with the lock issuer the isolated ones
// take theirs from.
//
// Without a shared store the issuer is one inside this process. That is honest
// for `migrate` on a laptop, and refuse keeps it out of every other
// environment.
func (k *consoleKernel) registry(out, errOut io.Writer, in io.Reader) *console.Application {
	locks := k.locks
	if locks == nil {
		locks = cache.NewLocks(cache.NewArrayStore())
	}
	registry := console.NewApplication(out, errOut, in).WithLocks(locks, isolationLockTTL)
	for _, d := range k.commands {
		registry.Add(d.command)
	}
	return registry
}

// refuse answers why a command may not run here, or nil.
//
// Only the isolated commands need a lock, so only they are refused for the
// want of one: migrate:status reads, and make:migration writes a file.
func (k *consoleKernel) refuse(c console.Command) error {
	if c.Isolated == "" || k.locks != nil || k.environment.Is(config.EnvDev) {
		return nil
	}
	return fmt.Errorf("%s takes a lock every replica can see, and no store every replica sees is configured: "+
		"a lock held inside this process is invisible to the replica beside it, so a rollout would run it "+
		"N times at once (this is APP_ENV=%s). Set CACHE_STORE=redis and REDIS_URL", c.Name, k.environment)
}

// help is the listing: every command a person can be told about, sorted by
// name, so the group prefix does the grouping.
func (k *consoleKernel) help() string {
	listed := make([]declared, 0, len(k.commands))
	width := 0
	for _, d := range k.commands {
		if d.command.Hidden {
			continue
		}
		listed = append(listed, d)
		width = max(width, len(d.command.Name))
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].command.Name < listed[j].command.Name })

	var b strings.Builder
	b.WriteString("commands:\n")
	for _, d := range listed {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, d.command.Name, d.command.Description)
	}
	return b.String()
}
