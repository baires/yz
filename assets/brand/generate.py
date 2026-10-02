"""Regenerate vector-first yz brand assets. Requires rsvg-convert on PATH."""
import math
import subprocess
from pathlib import Path
from xml.sax.saxutils import escape

ROOT = Path(__file__).resolve().parent
CYAN, GREEN, RED = "#5CE1E6", "#8BD49C", "#FF6B6B"
DARK, LIGHT, INK = "#171B23", "#E6EAF2", "#242B38"


def star(cx, cy):
    points = []
    for i in range(10):
        angle = -math.pi / 2 + i * math.pi / 5
        radius = 1.9 if i % 2 == 0 else 0.85
        points.append(f"{cx + math.cos(angle) * radius:.3f},{cy + math.sin(angle) * radius:.3f}")
    return '<polygon points="' + ' '.join(points) + '"/>'


def mascot(body=LIGHT, accent=CYAN, state="idle", signal=True):
    shape = f'<path fill="{body}" d="M6 13h3v9h14v-9h3v12H6z"/>'
    shape += f'<path fill="{accent}" d="M11 7h10v3H11z M8 10h4v3H8z M20 10h4v3h-4z M11 25h3v3h-3z M18 25h3v3h-3z"/>'
    eyes = '<circle cx="13" cy="16" r="1.3"/><circle cx="19" cy="16" r="1.3"/>'
    if state == "blink":
        eyes = '<path d="M11.5 15.5h3v1h-3z M17.5 15.5h3v1h-3z"/>'
    elif state == "success":
        eyes = star(13, 16) + star(19, 16)
    elif state == "error":
        eyes = '<path d="m11.5 14.5 3 3m0-3-3 3m6-3 3 3m0-3-3 3" fill="none" stroke="' + accent + '" stroke-width="1"/>'
    shape += f'<g fill="{accent}">{eyes}</g>'
    if signal:
        if state == "working":
            shape += f'<path fill="{accent}" d="M16 2l3 4h-6z"/>'
        elif state == "success":
            shape += f'<path fill="none" stroke="{accent}" stroke-width="1.2" d="m13.5 4 1.5 1.5 3.5-3.5"/>'
        elif state == "error":
            shape += f'<path fill="{accent}" d="M15.4 1h1.2v3h-1.2z M15.4 5h1.2v1.2h-1.2z"/>'
    return shape


def svg(name, contents, width=32, height=32):
    markup = f'<svg xmlns="http://www.w3.org/2000/svg" width="{width * 16}" height="{height * 16}" viewBox="0 0 {width} {height}" role="img" aria-labelledby="title"><title id="title">{escape(name)}</title>{contents}</svg>\n'
    (ROOT / (name + '.svg')).write_text(markup)


def png(name, size, output=None):
    subprocess.run(['rsvg-convert', '-w', str(size), '-o', str(ROOT / (output or f'{name}-{size}.png')), str(ROOT / (name + '.svg'))], check=True)


svg('mascot', mascot())
svg('mascot-light', mascot(body=INK))
svg('mascot-mono', mascot(body=INK, accent=INK))
svg('mascot-white', mascot(body='#FFFFFF', accent='#FFFFFF'))
for state, color in [('working', CYAN), ('success', GREEN), ('error', RED), ('blink', CYAN)]:
    svg('mascot-' + state, mascot(accent=color, state=state))
for name, background, body in [('icon', DARK, LIGHT), ('icon-light', '#F3F5F8', INK)]:
    svg(name, f'<rect width="32" height="32" rx="7" fill="{background}"/>' + mascot(body=body))
svg('favicon', f'<rect width="32" height="32" rx="7" fill="{DARK}"/>' + mascot(signal=False))
# The wordmark is geometry, not a font: it renders identically everywhere.
letters = 'M38 9h4v10h10V9h4v16h-4v4H40v-4h12v-2H38z M64 9h20v4L70 25h14v4H64v-4l14-12H64z'
for name, body, accent in [('logo-dark', LIGHT, CYAN), ('logo-light', INK, CYAN), ('logo-mono', INK, INK)]:
    svg(name, mascot(body=body, accent=accent) + f'<path fill="{body}" d="{letters}"/>', 92, 32)
for name in ['mascot', 'mascot-light', 'mascot-mono', 'mascot-white', 'mascot-working', 'mascot-success', 'mascot-error', 'mascot-blink']:
    for size in [256, 512, 1024]:
        png(name, size)
for name in ['icon', 'icon-light']:
    for size in [32, 64, 128, 256, 512, 1024]:
        png(name, size)
for size in [16, 32, 48]:
    png('favicon', size)
png('icon', 180, 'apple-touch-icon.png')
png('icon', 192, 'android-chrome-192x192.png')
png('icon', 512, 'android-chrome-512x512.png')
for name in ['logo-dark', 'logo-light', 'logo-mono']:
    for size in [736, 1472]:
        png(name, size)
# ICO container with PNG payloads; modern browsers support these frames.
import struct
frames = [(ROOT / f'favicon-{size}.png').read_bytes() for size in [16, 32, 48]]
offset = 6 + 16 * len(frames)
entries = []
for size, data in zip([16, 32, 48], frames):
    entries.append(struct.pack('<BBBBHHII', size, size, 0, 0, 1, 32, len(data), offset))
    offset += len(data)
(ROOT / 'favicon.ico').write_bytes(struct.pack('<HHH', 0, 1, len(frames)) + b''.join(entries) + b''.join(frames))
# A single preview showing variants at useful sizes.
preview = f'<rect width="180" height="112" fill="{DARK}"/>'
preview += '<g transform="translate(8 6)">' + mascot() + f'<path fill="{LIGHT}" d="{letters}"/></g>'
for i, (state, color) in enumerate([('idle', CYAN), ('working', CYAN), ('success', GREEN), ('error', RED), ('blink', CYAN)]):
    preview += f'<g transform="translate({8 + i * 33} 46)">' + mascot(accent=color, state=state) + '</g>'
    preview += f'<text x="{24 + i * 33}" y="83" fill="{LIGHT}" font-family="monospace" font-size="3" text-anchor="middle">{state}</text>'
preview += '<rect x="8" y="92" width="8" height="8" fill="' + CYAN + '"/>'
preview += '<rect x="62" y="92" width="8" height="8" fill="' + GREEN + '"/>'
preview += '<rect x="116" y="92" width="8" height="8" fill="' + RED + '"/>'
for x, color in [(19, CYAN), (73, GREEN), (127, RED)]:
    preview += f'<text x="{x}" y="98" fill="{LIGHT}" font-family="monospace" font-size="4">{color}</text>'
svg('preview', preview, 180, 112)
png('preview', 1440, 'preview.png')
card = f'<rect width="150" height="78.75" fill="{DARK}"/>'
card += '<g transform="translate(29 13)">' + mascot() + f'<path fill="{LIGHT}" d="{letters}"/></g>'
card += f'<text x="75" y="56" fill="{LIGHT}" font-family="monospace" font-size="3.2" text-anchor="middle">Share files as links, straight from your terminal.</text>'
card += f'<text x="75" y="65" fill="{CYAN}" font-family="monospace" font-size="2.5" text-anchor="middle">Cloudflare R2 · One command · One link</text>'
svg('social-card', card, 150, 78.75)
png('social-card', 1200, 'social-card-1200x630.png')
print('Generated brand assets in', ROOT)
