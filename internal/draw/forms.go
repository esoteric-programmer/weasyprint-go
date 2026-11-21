package draw

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// PDF forms, ported from WeasyPrint's add_forms: form controls with
// appearance: auto become interactive form fields.

type formInput struct {
	box  *layout.Box
	form *html.Node
	rect []float64
}

type formState struct {
	acro       pdf.Dict
	fields     pdf.Array
	fonts      pdf.Dict
	radios     map[*html.Node]map[string]pdf.Dict
	radioRefs  map[*html.Node]map[string]pdf.Ref
	byForm     map[*html.Node]map[string]pdf.Ref
	formOrder  map[*html.Node][]string
	widgetsFor map[*layout.Box]pdf.Ref
}

func isFormInput(b *layout.Box) bool {
	if b.Element == nil || b.Pseudo != "" || b.Anonymous || b.Style == nil ||
		b.Kind == layout.KLine || b.Kind == layout.KText {
		return false
	}
	if b.Style.Kw(css.PAppearance) != "auto" {
		return false
	}
	switch b.Element.Tag {
	case "button", "input", "select", "textarea":
		return true
	}
	return false
}

func formOf(n *html.Node) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Tag == "form" {
			return p
		}
	}
	return nil
}

// collectInputs returns the form inputs of a page in tree order.
func collectInputs(b *layout.Box, height float64, out *[]formInput) {
	if isFormInput(b) {
		x0, y0 := b.ContentX(), b.ContentY()
		x1, y1 := x0+b.W, y0+b.H
		*out = append(*out, formInput{b, formOf(b.Element),
			[]float64{x0 * 0.75, (height - y0) * 0.75, x1 * 0.75, (height - y1) * 0.75}})
	}
	if b.Marker != nil {
		collectInputs(b.Marker, height, out)
	}
	for _, ch := range b.Children {
		collectInputs(ch, height, out)
	}
}

// humanName returns the alternate name of a field: the text of its
// label, its title, placeholder or name.
func humanName(form, element *html.Node, name string) string {
	if form != nil {
		id := element.Get("id")
		var found string
		ok := false
		form.Walk(func(n *html.Node) bool {
			if ok || n.Type != html.ElementNode {
				return !ok
			}
			if n.Tag == "label" {
				isChild := false
				for p := element.Parent; p != nil; p = p.Parent {
					if p == n {
						isChild = true
					}
				}
				if (id != "" && n.Get("for") == id) || (isChild && element.Tag == "input") {
					found = strings.Join(strings.Fields(n.TextContent()), " ")
					ok = true
					return false
				}
			}
			return true
		})
		if ok {
			return found
		}
	}
	if t := element.Get("title"); t != "" {
		return t
	}
	if p := element.Get("placeholder"); p != "" {
		return p
	}
	return element.Get("name")
}

// addForms adds the form fields of a page.
func (r *Renderer) addForms(pi int, p *layout.Page, catalog pdf.Dict, pageAnnots *pdf.Array) {
	var inputs []formInput
	collectInputs(p.Box, p.Height, &inputs)
	if len(inputs) == 0 {
		return
	}
	fs := r.forms
	if fs == nil {
		fs = &formState{fonts: pdf.Dict{}, radios: map[*html.Node]map[string]pdf.Dict{},
			radioRefs: map[*html.Node]map[string]pdf.Ref{}, byForm: map[*html.Node]map[string]pdf.Ref{},
			formOrder: map[*html.Node][]string{}, widgetsFor: map[*layout.Box]pdf.Ref{}}
		fs.acro = pdf.Dict{"NeedAppearances": true, "DR": pdf.Dict{"Font": fs.fonts}}
		r.forms = fs
	}
	pageRef := r.pageRefs[pi]
	for i, in := range inputs {
		el, style := in.box.Element, in.box.Style
		inputType := strings.ToLower(el.Get("type"))
		value, hasValue := el.Attr("value")
		if !hasValue {
			value = "Yes"
		}
		name, hasName := el.Attr("name")
		if !hasName {
			name = fmt.Sprintf("unknown-%d-%d", pageRef, i)
		}
		human := humanName(in.form, el, name)
		fontSize := style.FontSize() * 0.75
		col := style.Color(css.PColor)
		colorOps := fmt.Sprintf("%s %s %s rg", pdf.FormatNumber(col.R), pdf.FormatNumber(col.G), pdf.FormatNumber(col.B))
		field := pdf.Dict{"Type": pdf.Name("Annot"), "Subtype": pdf.Name("Widget"), "Rect": in.rect,
			"P": pageRef, "F": 4, "T": pdf.String(name), "TU": pdf.String(human)}
		da := func() pdf.String {
			spec := layout.SpecFor(style)
			sh := r.shaper.Shape("x", spec)
			if len(sh.Glyphs) == 0 || sh.Glyphs[0].Face == nil {
				return pdf.String(colorOps)
			}
			e := r.fm.get(sh.Glyphs[0].Face.Font)
			e.full = true
			fs.fonts[e.name] = e.ref
			return pdf.String(colorOps + " /" + e.name + " " + pdf.FormatNumber(fontSize) + " Tf")
		}
		var ref pdf.Ref
		switch {
		case inputType == "radio" || inputType == "checkbox":
			w, h := in.rect[2]-in.rect[0], in.rect[1]-in.rect[3]
			cx, cy, rx, ry := w/2, h/2, w/2, h/2
			st := pdf.NewContent(r.opts.Compress)
			st.SetFillRGB(col.R, col.G, col.B)
			var group pdf.Dict
			if inputType == "radio" {
				if fs.radios[in.form] == nil {
					fs.radios[in.form] = map[string]pdf.Dict{}
					fs.radioRefs[in.form] = map[string]pdf.Ref{}
				}
				group = fs.radios[in.form][name]
				if group == nil {
					group = pdf.Dict{"FT": pdf.Name("Btn"), "Ff": 1<<14 + 1<<15, "T": pdf.String(name),
						"TU": pdf.String(human), "V": pdf.Name("Off"), "Kids": pdf.Array{}, "Opt": pdf.Array{}}
					fs.radios[in.form][name] = group
					gr := r.doc.Add(group)
					fs.radioRefs[in.form][name] = gr
					fs.fields = append(fs.fields, gr)
				}
				px, py := rx/math.Sqrt(math.Pi), ry/math.Sqrt(math.Pi)
				st.MoveTo(cx+rx, cy)
				st.CurveTo(cx+rx, cy+py, cx+px, cy+ry, cx, cy+ry)
				st.CurveTo(cx-px, cy+ry, cx-rx, cy+py, cx-rx, cy)
				st.CurveTo(cx-rx, cy-py, cx-px, cy-ry, cx, cy-ry)
				st.CurveTo(cx+px, cy-ry, cx+rx, cy-py, cx+rx, cy)
			} else {
				st.MoveTo(cx-rx, cy)
				st.LineTo(cx, cy-ry)
				st.LineTo(cx+rx, cy+ry)
				st.LineTo(cx-rx/7, cy-ry/3)
			}
			st.ClosePath()
			st.Fill()
			st.Dict = pdf.Dict{"Type": pdf.Name("XObject"), "Subtype": pdf.Name("Form"), "BBox": []float64{0, 0, w, h}}
			checkedRef := r.doc.Add(st)
			_, checked := el.Attr("checked")
			key := "on"
			if inputType == "radio" {
				key = fmt.Sprint(len(group["Kids"].(pdf.Array)))
			}
			field["FT"] = pdf.Name("Btn")
			if checked {
				field["AS"] = pdf.Name(key)
			} else {
				field["AS"] = pdf.Name("Off")
			}
			field["AP"] = pdf.Dict{"N": pdf.Dict{key: checkedRef}}
			field["MK"] = pdf.Dict{"BC": []float64{col.R, col.G, col.B}}
			field["Border"] = pdf.Array{0, 0, 0}
			ref = r.doc.Add(field)
			if inputType == "radio" {
				field["Parent"] = fs.radioRefs[in.form][name]
				if checked {
					group["V"] = pdf.Name(key)
				}
				group["Kids"] = append(group["Kids"].(pdf.Array), ref)
				group["Opt"] = append(group["Opt"].(pdf.Array), pdf.String(value))
			} else {
				field["V"] = field["AS"]
			}
		case el.Tag == "select":
			field["FT"] = pdf.Name("Ch")
			field["DA"] = da()
			var options pdf.Array
			var selected pdf.Array
			el.Walk(func(n *html.Node) bool {
				if n.Type == html.ElementNode && n.Tag == "option" {
					v := pdf.String(n.Get("value"))
					options = append(options, pdf.Array{v, pdf.String(n.TextContent())})
					if _, ok := n.Attr("selected"); ok {
						selected = append(selected, v)
					}
				}
				return true
			})
			field["Opt"] = options
			if _, ok := el.Attr("multiple"); ok {
				field["Ff"] = 1 << 21
				field["V"] = selected
			} else {
				field["Ff"] = 1 << 17
				if len(selected) > 0 {
					field["V"] = selected[len(selected)-1]
				} else {
					field["V"] = pdf.String("")
				}
			}
			ref = r.doc.Add(field)
		case inputType == "submit" || el.Tag == "button":
			// Submit buttons, like WeasyPrint, only for GET forms.
			if in.form == nil {
				continue
			}
			action := strings.TrimSpace(in.form.Get("action"))
			if action == "" || strings.ToLower(in.form.Get("method")) == "post" {
				continue
			}
			var fields pdf.Array
			for _, n := range fs.formOrder[in.form] {
				fields = append(fields, fs.byForm[in.form][n])
			}
			field["FT"] = pdf.Name("Btn")
			field["DA"] = pdf.String(colorOps)
			field["V"] = pdf.String(in.form.Get("value"))
			field["Ff"] = 1 << 16
			field["A"] = pdf.Dict{"Type": pdf.Name("Action"), "S": pdf.Name("SubmitForm"),
				"F": pdf.String(action), "Fields": fields, "Flags": 1<<2 + 1<<3}
			ref = r.doc.Add(field)
		default:
			// Text, password, textarea, file and unknown fields.
			field["FT"] = pdf.Name("Tx")
			field["DA"] = da()
			field["V"] = pdf.String(el.Get("value"))
			switch {
			case el.Tag == "textarea":
				field["Ff"] = 1 << 12
				field["V"] = pdf.String(el.TextContent())
			case inputType == "password":
				field["Ff"] = 1 << 13
			case inputType == "file":
				field["Ff"] = 1 << 20
			}
			if ml, err := strconv.Atoi(el.Get("maxlength")); err == nil && ml >= 0 {
				field["MaxLen"] = ml
			}
			ref = r.doc.Add(field)
		}
		fs.widgetsFor[in.box] = ref
		*pageAnnots = append(*pageAnnots, ref)
		if inputType != "radio" {
			fs.fields = append(fs.fields, ref)
		}
		if fs.byForm[in.form] == nil {
			fs.byForm[in.form] = map[string]pdf.Ref{}
		}
		if _, ok := fs.byForm[in.form][name]; !ok {
			fs.byForm[in.form][name] = ref
			fs.formOrder[in.form] = append(fs.formOrder[in.form], name)
		}
	}
}
