# HarfBuzz comparison

`hb.py` shapes the cases of a JSON file with the system's HarfBuzz
(through ctypes) and writes glyph ids, clusters, advances and offsets in
font units:

```sh
python3 hb.py cases.json ref.json
OTL_HB_CASES=cases.json OTL_HB_REF=ref.json go test ./internal/otl -run HarfBuzz
OTL_HB_ARAB_CASES=arab.json OTL_HB_ARAB_REF=arab_ref.json go test ./internal/text -run HarfBuzzArabic
```

A case is `{"font": path, "text": string, "features": ["smcp", "liga=0"…],
"lang": "tr", "script": "Latn"|"Hebr"|"Arab", "rtl": bool}`. With
HarfBuzz 8.3 and 25 fonts (Latin, Greek, Cyrillic and Hebrew texts with
features, combining marks, ligatures, fractions and code ligatures), all
684 cases match; for Arabic, results only differ by the mirroring of
brackets, done when drawing here.
