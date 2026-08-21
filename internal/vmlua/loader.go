// Package vmlua provides a sandboxed Lua runtime for homura's VM
// customization script (~/.config/homura/vm.lua). The Lua script runs on the
// host at build/config time under a sandbox that exposes only the `homura`
// table (plus a curated set of safe base/table/string functions), and produces
// (1) a config struct equivalent to the old VMConfig and (2) the generated
// Dockerfile content that used to live in Dockerfile.custom.
//
// The package intentionally does NOT import internal/vm to avoid a circular
// dependency. The VM implementation version is injected via the WithVersion
// option at call time.
package vmlua

import (
	"fmt"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// Result is the plain-primitive output of running a vm.lua script. None of the
// fields reference internal/vm types. Dockerfile() returns the generated
// Dockerfile content (nil when no homura.image block ran).
type Result struct {
	AllowPaths   []string
	Snapshot     []string
	MaxSnapshots *int
	StateDir     string
	CacheDir     string
	version      int
	builder      *Builder
}

// Dockerfile assembles the generated custom Dockerfile:
//
//	FROM homura-vm-ubuntu-base:v<N>
//	<builder lines...>
//
// It returns nil when no homura.image block ran, or when the implementation
// version was not injected via WithVersion.
func (r *Result) Dockerfile() []byte {
	if r == nil || r.builder == nil || r.version <= 0 {
		return nil
	}
	if len(r.builder.lines) == 0 {
		return []byte(fmt.Sprintf("FROM homura-vm-ubuntu-base:v%d\n", r.version))
	}
	return []byte(fmt.Sprintf("FROM homura-vm-ubuntu-base:v%d\n%s\n", r.version, strings.Join(r.builder.lines, "\n")))
}

// option holds the injected configuration for a Load call.
type option struct {
	version int
	home    string
}

// Option configures a Load call.
type Option func(*option)

// WithVersion injects the VM implementation version used to assemble the FROM
// line in Dockerfile(). It is provided by the internal/vm package at call
// time. Without it, Dockerfile() returns nil.
func WithVersion(v int) Option {
	return func(o *option) { o.version = v }
}

// WithHostHome sets the host home directory used by recipes like
// installPolytoken/installPi and the linkDotClaudeFromHost primitive. Defaults
// to the current user's home directory.
func WithHostHome(h string) Option {
	return func(o *option) { o.home = h }
}

// forbiddenBase are base-library globals that are explicitly removed after
// opening base/table/string, so a vm.lua cannot load more code or touch the
// process through them.
var forbiddenBase = []string{
	"dofile", "loadfile", "loadstring", "load", "require",
}

// Load runs the vm.lua script at path under a sandboxed Lua state and returns
// the resulting configuration. If the file does not exist, it returns an empty
// Result (defaults) and a nil error — matching the old "no vm.json" behavior.
func Load(path string, opts ...Option) (*Result, error) {
	opt := &option{home: defaultHome(), version: 0}
	for _, o := range opts {
		o(opt)
	}

	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()

	// Open only the curated subset of libraries. Never OpenOs/OpenIo/OpenDebug
	// /OpenPackage (which would register os/io/debug/package globals).
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)

	// Remove the remaining unsafe base globals so the sandbox cannot load new
	// code.
	for _, g := range forbiddenBase {
		L.SetGlobal(g, lua.LNil)
	}

	res := &Result{version: opt.version}
	builder := &Builder{hostHome: opt.home}

	if err := registerAPI(L, res, builder); err != nil {
		return nil, err
	}

	src, err := readScript(path)
	if err != nil {
		return nil, err
	}

	if err := L.DoString(src); err != nil {
		return nil, fmt.Errorf("failed to run vm.lua: %w", err)
	}

	res.mergeAllowPaths(builder)
	return res, nil
}

// readScript reads the script at path, returning "" (not an error) when the
// file does not exist.
func readScript(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// defaultHome returns the host home directory via $HOME.
func defaultHome() string {
	return os.Getenv("HOME")
}

// registerAPI builds the sandboxed `homura` global table (homura.config,
// homura.image) and binds the builder methods.
func registerAPI(L *lua.LState, res *Result, builder *Builder) error {
	homura := L.NewTable()
	L.SetGlobal("homura", homura)

	L.SetField(homura, "config", L.NewFunction(configFn(L, res)))
	L.SetField(homura, "image", L.NewFunction(imageFn(L, res, builder)))

	// Bind the builder's methods into a table shared by every `m` instance via
	// a metatable __index, so m:foo() calls resolve and self (index 1) is
	// skipped by the bound functions.
	methods := L.NewTable()
	for name, fn := range builderMethods(L, builder) {
		L.SetField(methods, name, L.NewFunction(fn))
	}
	mt := L.NewTable()
	L.SetField(mt, "__index", methods)

	// Keep a package-level reference so the metatable isn't garbage collected
	// while Lua functions still reference it.
	builderMeta = mt
	return nil
}

// builderMeta is the shared metatable for every `m` builder instance.
var builderMeta *lua.LTable

// configFn implements homura.config(table).
func configFn(L *lua.LState, res *Result) lua.LGFunction {
	return func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		readConfig(L, res, tbl)
		return 0
	}
}

// imageFn implements homura.image(function(m) ... end).
func imageFn(L *lua.LState, res *Result, builder *Builder) lua.LGFunction {
	return func(L *lua.LState) int {
		fn := L.CheckFunction(1)

		m := L.NewTable()
		L.SetMetatable(m, builderMeta)

		// Signal that an image block ran, so Dockerfile() becomes non-nil.
		res.builder = builder

		L.Push(fn)
		L.Push(m)
		L.Call(1, 0)
		return 0
	}
}

// readConfig copies the recognized fields from a homura.config table.
func readConfig(L *lua.LState, res *Result, tbl *lua.LTable) {
	if v := tbl.RawGetString("stateDir"); v != lua.LNil {
		res.StateDir = lua.LVAsString(v)
	}
	if v := tbl.RawGetString("cacheDir"); v != lua.LNil {
		res.CacheDir = lua.LVAsString(v)
	}
	if v := tbl.RawGetString("snapshot"); v != lua.LNil {
		res.Snapshot = tableToStringSlice(v)
	}
	if v := tbl.RawGetString("allowPaths"); v != lua.LNil {
		res.AllowPaths = append(res.AllowPaths, tableToStringSlice(v)...)
	}
	if v := tbl.RawGetString("maxSnapshots"); v != lua.LNil {
		n := int(lua.LVAsNumber(v))
		res.MaxSnapshots = &n
	}
}

// tableToStringSlice converts a Lua array table of strings to a Go slice.
func tableToStringSlice(v lua.LValue) []string {
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	var out []string
	tbl.ForEach(func(_, val lua.LValue) {
		if val != lua.LNil {
			out = append(out, lua.LVAsString(val))
		}
	})
	return out
}

// mergeAllowPaths appends the builder's symlink-registered paths (if any) to
// Result.AllowPaths, deduping by normalized path. The first occurrence wins.
func (r *Result) mergeAllowPaths(b *Builder) {
	if b == nil || len(b.extraPaths) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, p := range r.AllowPaths {
		seen[normalizePathSpec(p)] = true
	}
	for _, p := range b.extraPaths {
		if seen[normalizePathSpec(p)] {
			continue
		}
		seen[normalizePathSpec(p)] = true
		r.AllowPaths = append(r.AllowPaths, p)
	}
}

// normalizePathSpec strips a trailing :ro/:rw suffix so duplicates differing
// only in access mode collapse to a single entry.
func normalizePathSpec(spec string) string {
	if strings.HasSuffix(spec, ":ro") {
		return strings.TrimSuffix(spec, ":ro")
	}
	if strings.HasSuffix(spec, ":rw") {
		return strings.TrimSuffix(spec, ":rw")
	}
	return spec
}