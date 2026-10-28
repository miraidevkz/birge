# Birge brand

The mark is **Shenber** («Шеңбер», "circle"): six dots sitting in a circle, like the Circle tab of the app. Nobody has the head of the table, and the circle is open. Chosen by the product owner and the customer on 2026-10-28 from six options; the design language around it is [ADR-0025](../docs/adr/0025-warm-design-language.md).

## Files

Everything here is generated from [`brand.json`](brand.json) by [`render.py`](render.py); never edit the outputs by hand.

| File | Use |
| --- | --- |
| `svg/mark.svg` | the mark alone, `currentColor`: takes the colour of its context (documents, slides, the web) |
| `svg/icon.svg` | app icon with rounded corners (web, PWA, favicon, admin) |
| `svg/icon-dark.svg` | the icon for dark surfaces |
| `svg/icon-square.svg` | full-bleed square: iOS and maskable PWA icons, the OS rounds the corners |
| `svg/icon-adaptive-foreground.svg` | Android adaptive icon foreground, inside the 66/108 safe zone |
| `svg/icon-monochrome.svg` | Android 13+ themed icon, one colour tinted by the system |
| `apps/mobile/assets/brand/*.png`, `apps/*/public/icon*.png` | the PNG sizes the apps and stores need |

In the apps, `Logo` in `packages/ui` draws the mark from views in the theme's `primary` colour, so it follows light and dark mode without image files.

## Recolour

Edit the four colours in `brand.json` and run, from the repository root:

```sh
python3 branding/render.py          # SVGs (standard library only)
python3 branding/render.py --png    # SVGs and PNGs, needs Playwright (see the script's docstring)
```

Keep `primary` equal to `primary` in `packages/ui/tokens.js` and the Android `adaptiveIcon.backgroundColor` in `apps/mobile/app.config.ts`, and keep the icon's dots at 3:1 contrast or more against its ground.

## Rules

- **One shape at every size.** Six dots, same geometry from 16 px to 1024 px; nothing is dropped or simplified at small sizes.
- **Clear space** around the mark: at least one dot diameter on every side. Smallest size: 16 px.
- **Discretion.** The icon sits on a mother's home screen where others can see it. No medical or diagnosis symbols, no "SOS", no red, and store and PWA texts stay neutral ("Birge — together"), never naming the app's subject.
- Do not stretch, rotate, outline, add shadows or effects, or put the mark on a busy photo.
