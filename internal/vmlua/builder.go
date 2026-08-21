package vmlua

import (
	"fmt"
	"path/filepath"
	"sort"

	lua "github.com/yuin/gopher-lua"
)

// Builder accumulates Dockerfile lines and symlink-registered host paths as a
// homura.image block executes. Each method emits one or more Dockerfile RUN /
// ENV lines.
type Builder struct {
	lines      []string
	extraPaths []string // path specs (":ro" suffix for read-only) to merge into AllowPaths
	hostHome   string
}

// appendLine appends a Dockerfile line.
func (b *Builder) appendLine(line string) {
	b.lines = append(b.lines, line)
}

// Primitive: aptUpdate.
//   - RUN apt-get update
func (b *Builder) aptUpdate() {
	b.appendLine("RUN apt-get update")
}

// Primitive: aptInstall. Emits one RUN per package, alphabetically sorted
// within the call:
//
//	RUN apt-get install -y --no-install-recommends <pkg>
func (b *Builder) aptInstall(pkgs ...string) {
	sorted := append([]string(nil), pkgs...)
	sort.Strings(sorted)
	for _, pkg := range sorted {
		b.appendLine(fmt.Sprintf("RUN apt-get install -y --no-install-recommends %s", pkg))
	}
}

// Primitive: run. Emits a raw RUN line (escape hatch).
func (b *Builder) run(cmd string) {
	b.appendLine("RUN " + cmd)
}

// Primitive: env. Emits ENV k=v.
func (b *Builder) env(k, v string) {
	b.appendLine(fmt.Sprintf("ENV %s=%s", k, v))
}

// Primitive: profile. Appends a line to /etc/profile.
func (b *Builder) profile(line string) {
	b.appendLine(fmt.Sprintf("RUN echo '%s' >> /etc/profile", line))
}

// Primitive: fishProfile. Appends a line to /root/.config/fish/config.fish.
func (b *Builder) fishProfile(line string) {
	b.appendLine(fmt.Sprintf("RUN echo '%s' >> /root/.config/fish/config.fish", line))
}

// Primitive: curl. Downloads a URL to dest.
func (b *Builder) curl(url, dest string) {
	b.appendLine(fmt.Sprintf("RUN curl -fsSL %s -o %s", url, dest))
}

// Primitive: tarExtract. Extracts an archive into a directory.
func (b *Builder) tarExtract(archive, dir string) {
	b.appendLine(fmt.Sprintf("RUN tar -xf %s -C %s", archive, dir))
}

// SymlinkOpts controls symlinkFromHost behavior.
type SymlinkOpts struct {
	RW       bool // read-write symlink (default read-only). Also picks the AllowPaths access mode.
	NoExpose bool // do not auto-register the host path in AllowPaths.
}

// parseSymlinkOpts reads {rw=..., noExpose=...} from a Lua table (nil-safe).
func parseSymlinkOpts(v lua.LValue) *SymlinkOpts {
	if v == lua.LNil {
		return nil
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	o := &SymlinkOpts{}
	if val := tbl.RawGetString("rw"); val != lua.LNil {
		o.RW = lua.LVAsBool(val)
	}
	if val := tbl.RawGetString("noExpose"); val != lua.LNil {
		o.NoExpose = lua.LVAsBool(val)
	}
	return o
}

// Primitive: symlinkFromHost. Emits:
//
//	RUN ln -s /mnt/host/<hostPath> <guestPath>
//
// By default the hostPath is registered in AllowPaths as read-only; opts.rw
// makes it read-write; opts.noExpose skips the registration.
func (b *Builder) symlinkFromHost(hostPath, guestPath string, opts *SymlinkOpts) {
	ro := true
	expose := true
	if opts != nil {
		if opts.RW {
			ro = false
		}
		if opts.NoExpose {
			expose = false
		}
	}
	b.appendLine(fmt.Sprintf("RUN ln -s /mnt/host%s %s", hostPath, guestPath))
	if expose {
		spec := hostPath
		if ro {
			spec += ":ro"
		}
		b.extraPaths = append(b.extraPaths, spec)
	}
}

// Primitive: linkDotClaudeFromHost. Symlinks ~/.claude and ~/.claude.json from
// the host. These paths are already auto-exposed by homura, so no AllowPaths
// entry is added.
func (b *Builder) linkDotClaudeFromHost() {
	h := b.hostHome
	b.appendLine(fmt.Sprintf(
		"RUN rm -rf /root/.claude /root/.claude.json && ln -s /mnt/host%s/.claude /root/.claude && ln -s /mnt/host%s/.claude.json /root/.claude.json",
		h, h))
}

// builderMethods returns every builder method bound as a Lua function. Every
// bound function expects the `m` instance at index 1 (self) and reads its real
// arguments starting at index 2.
func builderMethods(L *lua.LState, b *Builder) map[string]lua.LGFunction {
	m := map[string]lua.LGFunction{}

	// m:aptUpdate()
	m["aptUpdate"] = noArgFn(b.aptUpdate)

	// m:aptInstall(pkg, ...)
	m["aptInstall"] = func(L *lua.LState) int {
		var pkgs []string
		n := L.GetTop()
		for i := 2; i <= n; i++ {
			pkgs = append(pkgs, L.CheckString(i))
		}
		b.aptInstall(pkgs...)
		return 0
	}

	// m:run(cmd)
	m["run"] = oneStrFn(L, func(s string) { b.run(s) })

	// m:env(k, v)
	m["env"] = func(L *lua.LState) int {
		b.env(L.CheckString(2), L.CheckString(3))
		return 0
	}

	// m:profile(line)
	m["profile"] = oneStrFn(L, func(s string) { b.profile(s) })

	// m:fishProfile(line)
	m["fishProfile"] = oneStrFn(L, func(s string) { b.fishProfile(s) })

	// m:curl(url, dest)
	m["curl"] = func(L *lua.LState) int {
		b.curl(L.CheckString(2), L.CheckString(3))
		return 0
	}

	// m:tarExtract(archive, dir)
	m["tarExtract"] = func(L *lua.LState) int {
		b.tarExtract(L.CheckString(2), L.CheckString(3))
		return 0
	}

	// m:symlinkFromHost(hostPath, guestPath, opts?)
	m["symlinkFromHost"] = func(L *lua.LState) int {
		hostPath := L.CheckString(2)
		guestPath := L.CheckString(3)
		opts := parseSymlinkOpts(L.Get(4))
		b.symlinkFromHost(hostPath, guestPath, opts)
		return 0
	}

	// m:linkDotClaudeFromHost()
	m["linkDotClaudeFromHost"] = noArgFn(b.linkDotClaudeFromHost)

	// -- Recipes (Phase 2) --
	m["addDockerUbuntuRepo"] = noArgFn(b.addDockerUbuntuRepo)
	// m:installHelix(version)
	m["installHelix"] = oneStrFn(L, func(s string) { b.installHelix(s) })
	// m:installGo(version, opts?)
	m["installGo"] = func(L *lua.LState) int {
		version := L.CheckString(2)
		opts := parseBoolOpts(L.Get(3))
		b.installGo(version, opts["downloadViaSystemGo"])
		return 0
	}
	// m:installElixir(opts?)
	// opts.hex and opts.rebar default to true when unspecified.
	m["installElixir"] = func(L *lua.LState) int {
		opts := parseBoolOpts(L.Get(2))
		hex, hexSet := opts["hex"]
		rebar, rebarSet := opts["rebar"]
		if !hexSet {
			hex = true
		}
		if !rebarSet {
			rebar = true
		}
		b.installElixir(hex, rebar)
		return 0
	}
	// m:installClaude(version)
	m["installClaude"] = oneStrFn(L, func(s string) { b.installClaude(s) })
	// m:installRust()
	m["installRust"] = noArgFn(b.installRust)
	// m:installPolytoken()
	m["installPolytoken"] = noArgFn(b.installPolytoken)
	// m:installPi()
	m["installPi"] = noArgFn(b.installPi)

	return m
}

// noArgFn wraps a zero-argument builder method as a Lua function.
func noArgFn(fn func()) lua.LGFunction {
	return func(L *lua.LState) int {
		fn()
		return 0
	}
}

// oneStrFn wraps a single-string builder method as a Lua function.
func oneStrFn(L *lua.LState, fn func(string)) lua.LGFunction {
	return func(L *lua.LState) int {
		fn(L.CheckString(2))
		return 0
	}
}

// parseBoolOpts reads an arbitrary boolean-flag table (nil-safe) into a map.
func parseBoolOpts(v lua.LValue) map[string]bool {
	out := map[string]bool{}
	if v == lua.LNil {
		return out
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return out
	}
	tbl.ForEach(func(k, val lua.LValue) {
		key := lua.LVAsString(k)
		out[key] = lua.LVAsBool(val)
	})
	return out
}

// hostPath joins a sub-path onto the builder's host home.
func (b *Builder) hostPath(parts ...string) string {
	joined := filepath.Join(append([]string{b.hostHome}, parts...)...)
	return joined
}