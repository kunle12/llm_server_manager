# Bundled typefaces

Self-hosted to avoid third-party font requests (the console design must work
offline and inside the embedded binary).

| Family | Files | Upstream | License |
| --- | --- | --- | --- |
| Saira / Saira Extra Condensed | `saira-var.woff2`, `saira-extra-condensed-*.woff2` | [Omnibus-Type/Saira](https://github.com/Omnibus-Type/Saira) | SIL OFL 1.1 — see `OFL-Saira.txt` |
| IBM Plex Mono | `ibm-plex-mono-400.woff2`, `ibm-plex-mono-500.woff2` | [IBM/plex](https://github.com/IBM/plex) | SIL OFL 1.1 — see `OFL-IBM-Plex-Mono.txt` |

The woff2 subsets come from the Google Fonts builds. Do not replace them
without updating these notices. The shipped `saira-var.woff2` has a `wght`
axis of 400–600; weights above 600 are synthesised by the browser.
