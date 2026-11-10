The theme selector ships these palettes in the binary. No theme downloads or
installer run are needed when changing the saved choice.

`palettes/*.xml` are copied byte-for-byte from Chroma v2.23.1's `styles/`
directory. Only the selected palette is parsed on demand; Catclip does not
import Chroma's eagerly initialized full style registry. `darcula.xml` is copied
from Chroma v2.27.0; it works with v2.23.1's style API. The dependency upgrade was
benchmarked separately and deferred because of a highlighting regression.

The four Go-defined palettes adapt upstream colors to Chroma token categories;
they do not promise the IDE's semantic highlighting:

- VS Code Dark+/Light+: https://github.com/microsoft/vscode/tree/main/extensions/theme-defaults/themes (MIT).
- JetBrains Light: https://github.com/JetBrains/intellij-community/blob/master/platform/platform-resources/src/DefaultColorSchemesManager.xml (Apache-2.0).
- Atom One Light: https://github.com/atom/one-light-syntax/blob/master/styles/colors.less (MIT).
- Darcula: https://github.com/alecthomas/chroma/blob/v2.27.0/styles/darcula.xml (MIT).

The indexed formatter adapter in `formatter.go` obtains color sequences from
Chroma v2.23.1 at first use. Its fallback order and per-line reset/resume
behavior follow Chroma’s `formatters/tty_indexed.go` and `tty_truecolour.go`
(MIT). It does not copy their color tables or alter lexer behavior.

Chroma license, covering the palettes and adapted formatter behavior:

Copyright (C) 2017 Alec Thomas

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


## Solarized (local port)

The local Solarized Dark/Light styles override Chroma's stock variants.
Canonical sRGB values, light/dark foreground/background pairs and xterm-256
fallback indices follow https://ethanschoonover.com/solarized/ (Values and
Usage & Development). Syntax roles are adapted from Ethan Schoonover's
original Vim Solarized groups:
https://github.com/altercation/vim-colors-solarized/blob/master/colors/solarized.vim

Copyright (c) 2011 Ethan Schoonover; MIT license in solarized.LICENSE.
256-color mode intentionally uses the author's documented approximations;
exact sRGB colors require a terminal that supports truecolor. The fallback
monotones include grays, unlike a terminal configured with Solarized's own
16-color ANSI palette. Catclip never rewrites terminal palette slots.
