package css

import _ "embed"

// The user agent and presentational hints stylesheets come from WeasyPrint
// (BSD 3-Clause License, see ua/LICENSE.weasyprint).

//go:embed ua/html5_ua.css
var uaCSS string

//go:embed ua/weasyprint_go.css
var uaExtraCSS string

//go:embed ua/html5_ph.css
var phCSS string

// UserAgentCSS returns the default user agent stylesheet.
func UserAgentCSS() string { return uaCSS + "\n" + uaExtraCSS }

// PresentationalHintsCSS returns the stylesheet of presentational hints
// that can be expressed in CSS.
func PresentationalHintsCSS() string { return phCSS }

//go:embed ua/html5_ua_form.css
var formCSS string

// FormsCSS returns the user agent stylesheet used for PDF forms.
func FormsCSS() string { return formCSS }
