package fonts

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A subset of fontconfig's configuration (fonts.conf), used to expand font
// family lists the way Pango and fontconfig do for WeasyPrint: <alias>
// rules and <match target="pattern"> rules testing and editing the family.
// Rules testing other properties (such as lang) are ignored.

// FamilyValue is a family name in an expanded family list.
type FamilyValue struct {
	Name   string
	Strong bool
}

type fcTest struct {
	qual    string // any, all
	compare string // eq, not_eq
	values  []string
}

type fcEdit struct {
	mode    string
	binding string // weak, strong, same
	values  []string
}

type fcRule struct {
	tests []fcTest
	edits []fcEdit
}

// FontConfig holds the family substitution rules of a fontconfig setup.
type FontConfig struct {
	rules []fcRule
	// Dirs are the font directories declared with <dir>.
	Dirs []string
}

// LoadFontConfig reads the fontconfig configuration of the system (or the
// file in $FONTCONFIG_FILE). It returns nil when there is none.
func LoadFontConfig() *FontConfig {
	path := os.Getenv("FONTCONFIG_FILE")
	if path == "" {
		dir := os.Getenv("FONTCONFIG_PATH")
		if dir == "" {
			dir = "/etc/fonts"
		}
		path = filepath.Join(dir, "fonts.conf")
	}
	fc := &FontConfig{}
	seen := map[string]bool{}
	if !fc.loadFile(path, seen, 0) {
		return nil
	}
	if len(fc.rules) == 0 && len(fc.Dirs) == 0 {
		return nil
	}
	return fc
}

type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []xmlNode  `xml:",any"`
	Text     string     `xml:",chardata"`
}

func (n *xmlNode) attr(name, def string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return def
}

func (fc *FontConfig) loadFile(path string, seen map[string]bool, depth int) bool {
	if depth > 10 || seen[path] {
		return false
	}
	seen[path] = true
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	if st.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".conf") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			fc.loadFile(filepath.Join(path, n), seen, depth+1)
		}
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var root xmlNode
	if err := dec.Decode(&root); err != nil {
		return false
	}
	base := filepath.Dir(path)
	for i := range root.Children {
		n := &root.Children[i]
		switch n.XMLName.Local {
		case "include":
			p := strings.TrimSpace(n.Text)
			if n.attr("prefix", "") == "xdg" {
				cfg := os.Getenv("XDG_CONFIG_HOME")
				if cfg == "" {
					home, _ := os.UserHomeDir()
					cfg = filepath.Join(home, ".config")
				}
				p = filepath.Join(cfg, p)
			} else if strings.HasPrefix(p, "~/") {
				home, _ := os.UserHomeDir()
				p = filepath.Join(home, p[2:])
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(base, p)
			}
			fc.loadFile(p, seen, depth+1)
		case "dir":
			p := strings.TrimSpace(n.Text)
			if n.attr("prefix", "") == "xdg" {
				data := os.Getenv("XDG_DATA_HOME")
				if data == "" {
					home, _ := os.UserHomeDir()
					data = filepath.Join(home, ".local", "share")
				}
				p = filepath.Join(data, p)
			} else if strings.HasPrefix(p, "~/") {
				home, _ := os.UserHomeDir()
				p = filepath.Join(home, p[2:])
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(base, p)
			}
			fc.Dirs = append(fc.Dirs, p)
		case "alias":
			fc.addAlias(n)
		case "match":
			fc.addMatch(n)
		}
	}
	return true
}

func familyStrings(n *xmlNode, tag string) []string {
	var out []string
	for i := range n.Children {
		c := &n.Children[i]
		if c.XMLName.Local == tag {
			out = append(out, strings.TrimSpace(c.Text))
		}
	}
	return out
}

func (fc *FontConfig) addAlias(n *xmlNode) {
	binding := n.attr("binding", "weak")
	families := familyStrings(n, "family")
	if len(families) == 0 {
		return
	}
	var edits []fcEdit
	for i := range n.Children {
		c := &n.Children[i]
		var mode string
		switch c.XMLName.Local {
		case "prefer":
			mode = "prepend"
		case "accept":
			mode = "append"
		case "default":
			mode = "append_last"
		default:
			continue
		}
		if vals := familyStrings(c, "family"); len(vals) > 0 {
			edits = append(edits, fcEdit{mode: mode, binding: binding, values: vals})
		}
	}
	if len(edits) == 0 {
		return
	}
	// Each family of the alias is a separate rule.
	for _, f := range families {
		fc.rules = append(fc.rules, fcRule{tests: []fcTest{{qual: "any", compare: "eq", values: []string{f}}}, edits: edits})
	}
}

func (fc *FontConfig) addMatch(n *xmlNode) {
	if t := n.attr("target", "pattern"); t != "pattern" {
		return
	}
	var r fcRule
	for i := range n.Children {
		c := &n.Children[i]
		switch c.XMLName.Local {
		case "test":
			if c.attr("name", "") != "family" || c.attr("target", "default") == "font" {
				return // tests on other properties are not supported
			}
			qual := c.attr("qual", "any")
			cmp := c.attr("compare", "eq")
			if (qual != "any" && qual != "all") || (cmp != "eq" && cmp != "not_eq") {
				return
			}
			vals := familyStrings(c, "string")
			if len(vals) != 1 || len(c.Children) != 1 {
				return
			}
			r.tests = append(r.tests, fcTest{qual: qual, compare: cmp, values: vals})
		case "edit":
			if c.attr("name", "") != "family" {
				continue // edits of other properties do not change families
			}
			vals := familyStrings(c, "string")
			if len(vals) != len(c.Children) {
				return // expressions are not supported
			}
			r.edits = append(r.edits, fcEdit{mode: c.attr("mode", "assign"), binding: c.attr("binding", "weak"), values: vals})
		}
	}
	if len(r.edits) > 0 {
		fc.rules = append(fc.rules, r)
	}
}

// familyEqual compares family names ignoring case and blanks, like
// fontconfig.
func familyEqual(a, b string) bool {
	return normFamily(a) == normFamily(b)
}

func normFamily(s string) string {
	s = strings.ToLower(s)
	if strings.ContainsAny(s, " \t") {
		s = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, s)
	}
	return s
}

// Expand applies the substitution rules to a family list, as
// FcConfigSubstitute does for a pattern.
func (fc *FontConfig) Expand(families []string) []FamilyValue {
	list := make([]FamilyValue, 0, len(families))
	for _, f := range families {
		list = append(list, FamilyValue{f, true})
	}
	for _, r := range fc.rules {
		matched := -1
		ok := true
		for _, t := range r.tests {
			v := t.values[0]
			switch t.qual {
			case "all":
				for _, f := range list {
					if familyEqual(f.Name, v) == (t.compare == "eq") {
						continue
					}
					ok = false
					break
				}
			default:
				found := -1
				for i, f := range list {
					if familyEqual(f.Name, v) == (t.compare == "eq") {
						found = i
						break
					}
				}
				if found < 0 {
					ok = false
				} else if matched < 0 {
					matched = found
				}
			}
			if !ok {
				break
			}
		}
		if !ok {
			continue
		}
		for _, e := range r.edits {
			strong := e.binding == "strong"
			if e.binding == "same" {
				strong = matched >= 0 && list[matched].Strong
			}
			vals := make([]FamilyValue, len(e.values))
			for i, v := range e.values {
				vals[i] = FamilyValue{v, strong}
			}
			mode := e.mode
			if matched < 0 {
				switch mode {
				case "prepend":
					mode = "prepend_first"
				case "append":
					mode = "append_last"
				case "assign", "delete":
					mode = mode + "_all"
				}
			}
			switch mode {
			case "prepend":
				list = append(list[:matched:matched], append(vals, list[matched:]...)...)
				matched += len(vals)
			case "append":
				list = append(list[:matched+1:matched+1], append(vals, list[matched+1:]...)...)
			case "prepend_first":
				list = append(vals, list...)
				if matched >= 0 {
					matched += len(vals)
				}
			case "append_last":
				list = append(list, vals...)
			case "assign":
				list = append(list[:matched:matched], append(vals, list[matched+1:]...)...)
				matched = -1
			case "assign_all", "assign_replace":
				list = vals
				matched = -1
			case "delete":
				list = append(list[:matched:matched], list[matched+1:]...)
				matched = -1
			case "delete_all":
				list = nil
				matched = -1
			}
		}
	}
	return list
}
