# TeX hyphenation patterns

The default hyphenation patterns for German and English, taken from the
[hyph-utf8](https://github.com/hyphenation/tex-hyphen) repository
(`hyph-utf8/tex/generic/hyph-utf8/patterns/txt`). Each `hyph-*.txt.gz` file
holds a pattern file (`hyph-*.pat.txt`), followed by the line `%exceptions`
and the exception list (`hyph-*.hyp.txt`) where the language has one. The
`hyph-*.header.txt` files are the headers of the corresponding
`hyph-*.tex` files, with copyright, license and the hyphenmin values.

| File                | Language                                  | Used for                       | Min. left/right | License       |
|---------------------|-------------------------------------------|--------------------------------|-----------------|---------------|
| `hyph-de-1996`      | German, reformed spelling (2024-02-28)    | `de`, `de-DE`, `de-AT`, `de-CH`… | 2/2           | MIT           |
| `hyph-de-1901`      | German, traditional spelling (2024-02-28) | `de-1901`, `de-DE-1901`…       | 2/2             | MIT           |
| `hyph-de-ch-1901`   | Swiss German, traditional spelling        | `de-CH-1901`, `de-LI-1901`     | 2/2             | MIT           |
| `hyph-en-us`        | American English (with exceptions)        | `en`, `en-US`, `en-CA`…        | 2/3             | Permissive (see header) |
| `hyph-en-gb`        | British English (with exceptions)         | `en-GB`, `en-AU`, `en-NZ`, `en-IE`, `en-ZA`, `en-IN` | 2/3 | MIT |

The German patterns are those of the
[dehyph-exptl](https://ctan.org/pkg/dehyph-exptl) package version 1.0
(2024-02-28) by the German hyphenation team (Trennmustermannschaft),
generated from the wortliste database: hyph-utf8 ships them unchanged, and
they are what babel loads for `\babelprovide[hyphenrules=ngerman-x-latest]`
(`dehyphn-x-2024-02-28.pat`, `dehypht-x-…`, `dehyphts-x-…`). Their data
license is `LICENSE-dehyph-exptl.txt`.
