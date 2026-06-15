#!/usr/bin/env python3
"""Render a captured terminal transcript into a self-contained SVG "terminal".

Usage: python demo/render_svg.py <transcript.txt> demo/demo.svg

The transcript is plain text; lines beginning with "$ " are treated as commands.
The output is a static SVG that renders anywhere (including GitHub), with no
external fonts or assets.
"""
import html
import re
import sys

# Catppuccin Mocha palette.
BG, HEADER, TEXT, DIM = "#1e1e2e", "#181825", "#cdd6f4", "#6c7086"
GREEN, RED, PEACH, YELLOW, BLUE = "#a6e3a1", "#f38ba8", "#fab387", "#f9e2af", "#89b4fa"

CHARW, LINEH, FS = 8.4, 21.0, 14.0
PAD_X, TOP = 18.0, 44.0


def esc(s: str) -> str:
    return html.escape(s, quote=False)


def spans(line: str) -> str:
    """Return SVG tspans for one line, with light syntax coloring."""
    if line.startswith("$ "):
        rest = line[2:]
        comment = ""
        if "#" in rest:
            i = rest.index("#")
            rest, comment = rest[:i], rest[i:]
        out = f'<tspan fill="{GREEN}">$ </tspan><tspan fill="{TEXT}">{esc(rest)}</tspan>'
        if comment:
            out += f'<tspan fill="{DIM}">{esc(comment)}</tspan>'
        return out

    stripped = line.lstrip()
    indent = line[: len(line) - len(stripped)]
    first = stripped.split("  ")[0] if stripped else ""
    color = {"missing": RED, "extra": PEACH, "modified": YELLOW}.get(first)
    if color:
        rest = stripped[len(first):]
        return f'<tspan>{esc(indent)}</tspan><tspan fill="{color}">{esc(first)}</tspan><tspan fill="{TEXT}">{esc(rest)}</tspan>'
    if line.startswith("No drift"):
        return f'<tspan fill="{GREEN}">{esc(line)}</tspan>'
    if re.match(r"^\d+ of \d+ tenants drifted", line):
        return f'<tspan fill="{PEACH}">{esc(line)}</tspan>'
    if line.startswith("--") or line.startswith("SET ") or line.startswith("CREATE "):
        return f'<tspan fill="{BLUE}">{esc(line)}</tspan>'
    return f'<tspan fill="{TEXT}">{esc(line)}</tspan>'


def main() -> None:
    src, dst = sys.argv[1], sys.argv[2]
    with open(src, encoding="utf-8") as f:
        lines = [ln.rstrip("\n") for ln in f.read().split("\n")]
    while lines and lines[-1] == "":
        lines.pop()

    cols = max((len(ln) for ln in lines), default=40)
    width = round(PAD_X * 2 + cols * CHARW)
    height = round(TOP + len(lines) * LINEH + 16)

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}" font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace">',
        f'<rect width="{width}" height="{height}" rx="10" fill="{BG}"/>',
        f'<rect width="{width}" height="30" rx="10" fill="{HEADER}"/>',
        f'<rect y="20" width="{width}" height="10" fill="{HEADER}"/>',
        '<circle cx="18" cy="15" r="6" fill="#f38ba8"/>',
        '<circle cx="38" cy="15" r="6" fill="#f9e2af"/>',
        '<circle cx="58" cy="15" r="6" fill="#a6e3a1"/>',
        f'<text x="{width/2}" y="19" fill="{DIM}" font-size="12" text-anchor="middle">pgfleet drift demo</text>',
    ]
    y = TOP
    for ln in lines:
        parts.append(
            f'<text x="{PAD_X}" y="{y:.0f}" font-size="{FS}" xml:space="preserve">{spans(ln)}</text>'
        )
        y += LINEH
    parts.append("</svg>")
    with open(dst, "w", encoding="utf-8") as f:
        f.write("\n".join(parts) + "\n")
    print(f"wrote {dst} ({width}x{height}, {len(lines)} lines)")


if __name__ == "__main__":
    main()
