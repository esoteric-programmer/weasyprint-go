# TeX hyphenation patterns

The default hyphenation patterns, taken from the
[hyph-utf8](https://github.com/hyphenation/tex-hyphen) repository
(`hyph-utf8/tex/generic/hyph-utf8/patterns`). Each `hyph-*.txt.gz` file
holds a pattern file (`txt/hyph-*.pat.txt`), followed by the line
`%exceptions` and the exception list (`txt/hyph-*.hyp.txt`) where the
language has one (`nn` uses the identical `nb` file). The
`hyph-*.header.txt` files are the headers of the corresponding
`tex/hyph-*.tex` files, with copyright, license, version and the
`\lefthyphenmin`/`\righthyphenmin` values used for typesetting, which are
the defaults for `hyphenate-limit-chars: auto`. `texsets.go` lists them.

The German patterns are those of the
[dehyph-exptl](https://ctan.org/pkg/dehyph-exptl) package version 1.0
(2024-02-28) by the German hyphenation team (Trennmustermannschaft),
generated from the wortliste database: hyph-utf8 ships them unchanged, and
they are what babel loads for `\babelprovide[hyphenrules=ngerman-x-latest]`
(`dehyphn-x-2024-02-28.pat`, `dehypht-x-…`, `dehyphts-x-…`). Their data
license is `LICENSE-dehyph-exptl.txt`.

Language tags map to pattern sets like babel does: `de` (reformed),
`de-1901`, `de-CH-1901`; `en`/`en-US`, `en-GB` (also `en-AU`, `en-NZ`,
`en-IE`, `en-ZA`, `en-IN`); `el` (monotonic), `el-polyton`; `grc`;
`la`, `la-x-classic`, `la-x-liturgic`; `fi`, `fi-x-school`; `no`/`nb`, `nn`;
`sr`/`sr-Cyrl` and `sh-Cyrl` (babel's serbianc), `sr-Latn`/`sh`; `mn`;
`am`, `ti`, `gez` (Ethiopic); `zh-Latn` (pinyin); others by their language
subtag.

Not included, because their only license is the GPL or none is stated:
Czech (`cs`), Irish (`ga`), Indonesian (`id`), Macedonian (`mk`), the
newer Serbian Cyrillic set (`sr-cyrl`) and Romanian (`ro`). Czech,
Indonesian and Romanian use Pyphen's patterns.

| Set | Language | Min. left/right | License |
|-----|----------|-----------------|---------|
| `af` | Afrikaans | 1/2 | MIT |
| `as` | Assamese | 1/1 | MIT |
| `be` | Belarusian | 2/2 | MIT |
| `bg` | Bulgarian | 2/2 | see header |
| `bn` | Bengali | 1/1 | MIT |
| `ca` | Catalan | 2/2 | LPPL |
| `cop` | Coptic | 1/1 | MIT |
| `cu` | Church Slavonic | 1/2 | MIT |
| `cy` | Welsh | 2/3 | LPPL; MIT |
| `da` | Danish | 2/2 | LPPL; MIT |
| `de-1901` | Deutschsprachige Trennmustermannschaft | 2/2 | MIT |
| `de-1996` | Deutschsprachige Trennmustermannschaft | 2/2 | MIT |
| `de-ch-1901` | Deutschsprachige Trennmustermannschaft | 2/2 | MIT |
| `el-monoton` | Modern Greek, monotonic spelling | 1/1 | LPPL; MIT |
| `el-polyton` | Modern Greek, polytonic spelling | 1/1 | LPPL; MIT |
| `en-gb` | English, British spelling | 2/3 | MIT |
| `en-us` | English, American spelling | 2/3 | see header |
| `eo` | Esperanto | 2/2 | LPPL |
| `es` | Spanish | 2/2 | MIT/X11 |
| `et` | Estonian | 2/3 | MIT |
| `eu` | Basque | 2/2 | see header |
| `fi` | Finnish | 2/2 | see header |
| `fi-x-school` | Finnish, school hyphenation | 1/1 | MIT |
| `fr` | French | 2/2 | MIT |
| `fur` | Friulan | 2/2 | MIT |
| `gl` | Galician | 2/2 | Unlicence |
| `grc` | Ancient Greek | 1/1 | LPPL; MIT |
| `gu` | Gujarati | 1/1 | MIT |
| `hi` | Hindi | 1/1 | MIT |
| `hr` | Croatian | 2/2 | LPPL |
| `hsb` | Upper Sorbian | 2/2 | LPPL |
| `hu` | Hungarian | 2/2 | MPL; GPL; LGPL |
| `hy` | Armenian | 1/2 | LGPL |
| `ia` | Interlingua | 2/2 | LPPL |
| `is` | Icelandic | 2/2 | LPPL |
| `it` | Italian | 2/2 | LPPL; MIT |
| `ka` | Georgian | 1/2 | MIT |
| `kk` | Kazakh | 2/2 | Public domain |
| `kmr` | Kurmanji, Northern Kurdish | 2/2 | LPPL |
| `kn` | Kannada | 1/1 | MIT |
| `la` | Latin | 2/2 | MIT |
| `la-x-classic` | Classical Latin | 2/2 | MIT |
| `la-x-liturgic` | Liturgical Latin | 2/2 | MIT |
| `lt` | Lithuanian | 2/2 | MIT |
| `lv` | Latvian | 2/2 | LGPL; GPL |
| `ml` | Malayalam | 1/1 | MIT |
| `mn-cyrl` | Mongolian, Cyrillic script | 2/2 | LPPL; MIT |
| `mr` | Marathi | 1/1 | MIT |
| `mul-ethi` | Multiple languages using the Ethiopic script | 1/1 | MIT |
| `nb` | Norwegian Bokmål, bokmål, norsk bokmål | 2/2 | see header |
| `nl` | Dutch | 2/2 | MIT; Piet Tutelaers |
| `nn` | Norwegian Nynorsk, nynorsk | 2/2 | see header |
| `oc` | Occitan | 2/2 | MIT |
| `or` | Odia, Oriya | 1/1 | MIT |
| `pa` | Panjabi, Punjabi | 1/1 | MIT |
| `pi` | Pāli | 1/2 | MIT |
| `pl` | Polish | 2/2 | MIT |
| `pms` | Piedmontese | 2/2 | MIT |
| `pt` | Portuguese | 2/3 | BSD 3-clause licence |
| `rm` | Romansh | 2/2 | MIT |
| `ru` | Russian | 2/2 | LPPL |
| `sa` | Sanskrit | 1/3 | see header |
| `sh-cyrl` | Serbocroatian, Cyrillic script | 1/2 | LPPL |
| `sh-latn` | Serbocroatian, Latin script | 2/2 | LPPL |
| `sk` | Slovak | 2/3 | MIT |
| `sl` | Slovenian | 2/2 | LPPL; MIT |
| `sq` | Claudio Beccari | 2/2 | MIT |
| `sv` | Swedish | 2/2 | LPPL |
| `ta` | Tamil | 1/1 | MIT |
| `te` | Telugu | 1/1 | MIT |
| `th` | Thai | 2/3 | LPPL |
| `tk` | Turkmen | 2/2 | MIT |
| `tr` | Turkish | 2/2 | LPPL |
| `uk` | Ukrainian | 2/2 | MIT; LPPL |
| `zh-latn-pinyin` | Werner Lemberg | 1/2 | MIT |
