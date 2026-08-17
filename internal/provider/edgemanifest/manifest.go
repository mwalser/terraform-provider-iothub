package edgemanifest

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// What the function emits, fixed on purpose: the lowest schema versions that
// express everything below, accepted by every IoT Edge runtime since 1.0.10
// (the agent rejects anything above 1.1). Defaults follow the manifests
// Microsoft's tools generate, so a manifest moved from a template to this
// function comes out identical.
const (
	agentSchemaVersion = "1.1"
	hubSchemaVersion   = "1.1"

	defaultStatus        = "running"
	defaultRestartPolicy = "always"
	defaultTTLSeconds    = 7200
	defaultCreateOptions = "{}"
	// The port bindings every reference manifest gives edgeHub, so that
	// downstream devices can reach it.
	defaultHubCreateOptions = `{"HostConfig":{"PortBindings":{"443/tcp":[{"HostPort":"443"}],"5671/tcp":[{"HostPort":"5671"}],"8883/tcp":[{"HostPort":"8883"}]}}}`

	// createOptions is stored as a string and split the way the edge agent
	// itself writes it: 512-character chunks createOptions, createOptions01,
	// … createOptions99.
	createOptionsChunk  = 512
	createOptionsChunks = 100

	maxUint32 = 4294967295
	maxInt32  = 2147483647
	maxInt64  = 9223372036854775807
)

var (
	statuses        = []string{"running", "stopped"}
	restartPolicies = []string{"always", "on-failure", "on-unhealthy", "never"}
	pullPolicies    = []string{"on-create", "never"}

	// The edge agent's own image check (DockerConfig.ValidateAndGetImage).
	imageRe = regexp.MustCompile(`^(([^/]*/)*)([^/:]+)(:[^/:]+)?$`)
	// Module names as Microsoft's deployment schema allows them.
	moduleNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	// Environment variable names as Microsoft's deployment schema allows them.
	envNameRe = regexp.MustCompile(`^[^+#$\s.]+$`)
	// The route grammar's skeleton: [SELECT *] FROM /path [WHERE …] INTO sink,
	// keywords case-insensitive, the sink $upstream or BrokeredEndpoint("…").
	routeRe = regexp.MustCompile(`(?is)^\s*(select\s+\*\s+)?from\s+/\S*(\s+where\s+.+?)?\s+into\s+(\$upstream|brokeredendpoint\(\s*"([^"]*)"\s*\))\s*$`)
)

// manifest is the validated input.
type manifest struct {
	layered   bool
	creds     map[string]credential
	edgeAgent *systemModule
	edgeHub   *systemModule
	modules   map[string]*module
	routes    map[string]route
	ttl       int64
	maxSize   *int64
}

type credential struct{ address, username, password string }

// systemModule is edgeAgent or edgeHub. Status and restart policy are fixed
// by the agent for both (edgeAgent ignores them; edgeHub runs with restart
// policy always in every reference manifest).
type systemModule struct {
	image         string
	createOptions string // canonical JSON
	env           map[string]any
	pullPolicy    string
	startupOrder  *int64 // edgeHub only
}

type module struct {
	image         string
	createOptions string
	env           map[string]any
	status        string
	restartPolicy string
	pullPolicy    string
	startupOrder  *int64
	version       string
	desired       map[string]any
}

type route struct {
	route    string
	priority *int64
	ttl      *int64
}

// Build validates a decoded manifest object and returns the modulesContent
// document as canonical JSON, or every problem found.
func Build(v any) (string, []string) {
	var p problems
	m := read(&p, v)
	if len(p) > 0 {
		return "", p
	}
	content := m.content()
	// A last gate: whatever the module documents contain must be valid twin
	// content, or the hub rejects the deployment with less to go on.
	for _, name := range sortedKeys(content) {
		doc, ok := content[name].(map[string]any)
		if !ok {
			continue
		}
		for _, k := range sortedKeys(doc) {
			obj, ok := doc[k].(map[string]any)
			if !ok {
				continue
			}
			for _, prob := range twinpatch.Validate(obj) {
				p.add(name+"."+k, "%s", prob.String())
			}
		}
	}
	if len(p) > 0 {
		return "", p
	}
	return twinpatch.Encode(content), nil
}

// read turns the decoded argument into a manifest, reporting every problem.
func read(p *problems, v any) *manifest {
	root, ok := asObject(p, "", v)
	if !ok {
		return nil
	}
	m := &manifest{
		creds:   map[string]credential{},
		modules: map[string]*module{},
		routes:  map[string]route{},
		ttl:     defaultTTLSeconds,
	}
	m.layered, _ = root.boolean("layered")

	if creds, path, ok := root.entries("registry_credentials"); ok {
		for _, label := range sortedKeys(creds) {
			cp := entryPath(path, label)
			if msg := twinpatch.CheckKey(label); msg != "" {
				p.add(cp, "%s", msg)
			}
			if o, ok := asObject(p, cp, creds[label]); ok {
				var c credential
				o.require("address", "username", "password")
				c.address, _ = o.nonBlank("address")
				if strings.IndexFunc(c.address, unicode.IsSpace) >= 0 {
					o.p.add(o.child("address"), "must not contain whitespace")
				}
				c.username, _ = o.nonBlank("username")
				c.password, _ = o.nonBlank("password")
				o.finish()
				m.creds[label] = c
			}
		}
	}

	if o, ok := root.object("edge_agent"); ok {
		m.edgeAgent = readSystemModule(o, false)
	}
	if o, ok := root.object("edge_hub"); ok {
		m.edgeHub = readSystemModule(o, true)
	}

	if mods, path, ok := root.entries("modules"); ok {
		for _, name := range sortedKeys(mods) {
			mp := entryPath(path, name)
			switch {
			case name == "edgeAgent" || name == "edgeHub":
				p.add(mp, "%q is a system module; configure it with edge_agent or edge_hub", name)
			case !moduleNameRe.MatchString(name):
				p.add(mp, "module names may only contain letters, digits, '-' and '_'")
			}
			if o, ok := asObject(p, mp, mods[name]); ok {
				m.modules[name] = readModule(o)
			}
		}
	}

	if routes, path, ok := root.entries("routes"); ok {
		for _, name := range sortedKeys(routes) {
			rp := entryPath(path, name)
			if msg := twinpatch.CheckKey(name); msg != "" {
				p.add(rp, "%s", msg)
			}
			m.routes[name] = readRoute(p, rp, routes[name])
		}
	}

	if o, ok := root.object("store_and_forward"); ok {
		if ttl, ok := o.integer("time_to_live_secs", -1, maxInt32); ok {
			m.ttl = ttl
		}
		if n, ok := o.integer("max_size_bytes", 1, maxInt64); ok {
			m.maxSize = &n
		}
		o.finish()
	}

	if m.layered {
		for _, key := range []string{"edge_agent", "edge_hub", "registry_credentials", "store_and_forward"} {
			if _, ok := root.entry(key); ok {
				p.add(key, "not allowed in a layered manifest: only modules and routes are added on top of the base deployment")
			}
		}
	} else {
		if m.edgeAgent == nil {
			p.add("edge_agent", "required (the image of the IoT Edge agent)")
		}
		if m.edgeHub == nil {
			p.add("edge_hub", "required (the image of the IoT Edge hub)")
		}
	}
	root.finish()
	return m
}

func readSystemModule(o *object, hub bool) *systemModule {
	s := &systemModule{createOptions: defaultCreateOptions}
	if hub {
		s.createOptions = defaultHubCreateOptions
	}
	s.image = readImage(o)
	if co, ok := readCreateOptions(o); ok {
		s.createOptions = co
	}
	s.env = readEnv(o)
	s.pullPolicy, _ = o.enum("image_pull_policy", pullPolicies...)
	if hub {
		if n, ok := o.integer("startup_order", 0, maxUint32); ok {
			s.startupOrder = &n
		}
	}
	o.finish()
	return s
}

func readModule(o *object) *module {
	m := &module{createOptions: defaultCreateOptions, status: defaultStatus, restartPolicy: defaultRestartPolicy}
	m.image = readImage(o)
	if co, ok := readCreateOptions(o); ok {
		m.createOptions = co
	}
	m.env = readEnv(o)
	if s, ok := o.enum("status", statuses...); ok {
		m.status = s
	}
	if r, ok := o.enum("restart_policy", restartPolicies...); ok {
		m.restartPolicy = r
	}
	m.pullPolicy, _ = o.enum("image_pull_policy", pullPolicies...)
	if n, ok := o.integer("startup_order", 0, maxUint32); ok {
		m.startupOrder = &n
	}
	m.version, _ = o.str("version")
	if d, ok := o.object("desired"); ok {
		for _, prob := range twinpatch.Validate(d.m) {
			o.p.add(d.path, "%s", prob.String())
		}
		m.desired = d.m
	}
	o.finish()
	return m
}

func readImage(o *object) string {
	o.require("image")
	image, ok := o.nonBlank("image")
	if !ok {
		return ""
	}
	if strings.IndexFunc(image, unicode.IsSpace) >= 0 || !imageRe.MatchString(image) {
		o.p.add(o.child("image"), "not a container image reference ([registry/]repository[:tag])")
	}
	return image
}

// readCreateOptions accepts an object or a JSON object as a string and
// returns it as canonical JSON.
func readCreateOptions(o *object) (string, bool) {
	v, ok := o.entry("create_options")
	if !ok {
		return "", false
	}
	path := o.child("create_options")
	var doc map[string]any
	switch t := v.(type) {
	case map[string]any:
		doc = t
	case string:
		d, err := twinpatch.Decode(t)
		if err != nil {
			o.p.add(path, "%v", err)
			return "", false
		}
		doc = d
	default:
		o.p.add(path, "must be an object or a JSON string, got %s", kindOf(v))
		return "", false
	}
	s := twinpatch.Encode(toJSONMap(doc))
	if n := len([]rune(s)); n > createOptionsChunk*createOptionsChunks {
		o.p.add(path, "is %d characters; the limit is %d", n, createOptionsChunk*createOptionsChunks)
		return "", false
	}
	return s, true
}

func readEnv(o *object) map[string]any {
	env, path, ok := o.entries("env")
	if !ok {
		return nil
	}
	out := make(map[string]any, len(env))
	for _, name := range sortedKeys(env) {
		ep := entryPath(path, name)
		if !envNameRe.MatchString(name) {
			o.p.add(ep, "environment variable names must not contain '.', '$', '#', '+' or whitespace")
		}
		switch v := env[name].(type) {
		case string, bool:
			out[name] = v
		case *big.Float:
			out[name] = numberOf(v)
		case nil:
			o.p.add(ep, "must be a string, number or bool, got null")
		default:
			o.p.add(ep, "must be a string, number or bool, got %s", kindOf(v))
		}
	}
	return out
}

func readRoute(p *problems, path string, v any) route {
	var r route
	switch t := v.(type) {
	case string:
		r.route = t
		checkRoute(p, path, t)
	case map[string]any:
		o, _ := asObject(p, path, t)
		o.require("route")
		if s, ok := o.nonBlank("route"); ok {
			r.route = s
			checkRoute(p, o.child("route"), s)
		}
		if n, ok := o.integer("priority", 0, 9); ok {
			r.priority = &n
		}
		if n, ok := o.integer("time_to_live_secs", 0, maxUint32); ok {
			r.ttl = &n
		}
		o.finish()
	default:
		p.add(path, "must be a route string or an object with route, priority and time_to_live_secs, got %s", kindOf(v))
	}
	return r
}

// checkRoute verifies the skeleton of a route; the condition grammar is left
// to the hub.
func checkRoute(p *problems, path, s string) {
	m := routeRe.FindStringSubmatch(s)
	if m == nil {
		p.add(path, `must have the form FROM <source> [WHERE <condition>] INTO $upstream or INTO BrokeredEndpoint("/modules/<module>/inputs/<input>")`)
		return
	}
	if endpoint := m[4]; strings.HasPrefix(strings.ToLower(m[3]), "brokered") {
		parts := strings.Split(strings.Trim(endpoint, "/"), "/")
		if len(parts) != 4 || parts[1] == "" || parts[3] == "" {
			p.add(path, `the BrokeredEndpoint must be "/modules/<module>/inputs/<input>", got %q`, endpoint)
		}
	}
}

// content builds the modulesContent document.
func (m *manifest) content() map[string]any {
	out := map[string]any{}
	if m.layered {
		agent := map[string]any{}
		for name, mod := range m.modules {
			agent["properties.desired.modules."+name] = mod.json()
		}
		out["$edgeAgent"] = agent
		if len(m.routes) > 0 {
			hub := map[string]any{}
			for name, r := range m.routes {
				hub["properties.desired.routes."+name] = r.json()
			}
			out["$edgeHub"] = hub
		}
	} else {
		creds := map[string]any{}
		for label, c := range m.creds {
			creds[label] = map[string]any{"address": c.address, "username": c.username, "password": c.password}
		}
		modules := map[string]any{}
		for name, mod := range m.modules {
			modules[name] = mod.json()
		}
		out["$edgeAgent"] = map[string]any{
			"properties.desired": map[string]any{
				"schemaVersion": agentSchemaVersion,
				"runtime": map[string]any{
					"type":     "docker",
					"settings": map[string]any{"registryCredentials": creds},
				},
				"systemModules": map[string]any{
					"edgeAgent": m.edgeAgent.json(false),
					"edgeHub":   m.edgeHub.json(true),
				},
				"modules": modules,
			},
		}
		routes := map[string]any{}
		for name, r := range m.routes {
			routes[name] = r.json()
		}
		saf := map[string]any{"timeToLiveSecs": m.ttl}
		if m.maxSize != nil {
			saf["storeLimits"] = map[string]any{"maxSizeBytes": *m.maxSize}
		}
		out["$edgeHub"] = map[string]any{
			"properties.desired": map[string]any{
				"schemaVersion":                hubSchemaVersion,
				"routes":                       routes,
				"storeAndForwardConfiguration": saf,
			},
		}
	}
	for name, mod := range m.modules {
		if mod.desired != nil {
			out[name] = map[string]any{"properties.desired": toJSONMap(mod.desired)}
		}
	}
	return out
}

func (s *systemModule) json(hub bool) map[string]any {
	out := map[string]any{"type": "docker", "settings": settings(s.image, s.createOptions)}
	if hub {
		out["status"] = defaultStatus
		out["restartPolicy"] = defaultRestartPolicy
	}
	if len(s.env) > 0 {
		out["env"] = envJSON(s.env)
	}
	if s.pullPolicy != "" {
		out["imagePullPolicy"] = s.pullPolicy
	}
	if s.startupOrder != nil {
		out["startupOrder"] = *s.startupOrder
	}
	return out
}

func (m *module) json() map[string]any {
	out := map[string]any{
		"type":          "docker",
		"status":        m.status,
		"restartPolicy": m.restartPolicy,
		"settings":      settings(m.image, m.createOptions),
	}
	if len(m.env) > 0 {
		out["env"] = envJSON(m.env)
	}
	if m.pullPolicy != "" {
		out["imagePullPolicy"] = m.pullPolicy
	}
	if m.startupOrder != nil {
		out["startupOrder"] = *m.startupOrder
	}
	if m.version != "" {
		out["version"] = m.version
	}
	return out
}

func (r route) json() any {
	if r.priority == nil && r.ttl == nil {
		return r.route
	}
	out := map[string]any{"route": r.route}
	if r.priority != nil {
		out["priority"] = *r.priority
	}
	if r.ttl != nil {
		out["timeToLiveSecs"] = *r.ttl
	}
	return out
}

// settings builds a module's settings object with createOptions split into
// chunks when it does not fit into one.
func settings(image, createOptions string) map[string]any {
	out := map[string]any{"image": image}
	for i, chunk := range chunks(createOptions) {
		key := "createOptions"
		if i > 0 {
			key = fmt.Sprintf("createOptions%02d", i)
		}
		out[key] = chunk
	}
	return out
}

func chunks(s string) []string {
	r := []rune(s)
	var out []string
	for len(r) > createOptionsChunk {
		out = append(out, string(r[:createOptionsChunk]))
		r = r[createOptionsChunk:]
	}
	return append(out, string(r))
}

func envJSON(env map[string]any) map[string]any {
	out := make(map[string]any, len(env))
	for name, v := range env {
		out[name] = map[string]any{"value": v}
	}
	return out
}

func toJSONMap(doc map[string]any) map[string]any {
	m, ok := jsonValue(doc).(map[string]any)
	if !ok { // jsonValue maps objects to objects
		return map[string]any{}
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Format joins problems into one error text.
func Format(problems []string) string {
	if len(problems) == 1 {
		return problems[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems:", len(problems))
	for _, p := range problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}
