# Brand assets

The mark is a sabiá-laranjeira, Brazil's national bird, whose wing is made of table rows.

| File | Use |
|---|---|
| `icon.svg`, `icon-512.png` | the icon: favicon, app, GitHub avatar, social profiles |
| `logo-mark.svg` | the bird alone, on any background: docs site header |
| `logo.svg` | the bird with the name: README and anywhere the name is needed |

Colors: orange `#E8762B`, dark brown `#3B2415`, brown `#6B4226`, cream `#F5E6C8`, beak `#C9922E`.

`web/public/favicon.svg`, `docs/site/public/favicon.svg` and `web/src/components/layout/BrandMark.vue`
repeat the icon; change them together. Regenerate the PNGs with
`magick -background none -density 1200 icon.svg -resize 512x512 icon-512.png`.
