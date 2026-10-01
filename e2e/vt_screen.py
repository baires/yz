"""Replay the cursor and erase operations emitted by the TUI test renderer.

Raw transcripts alone miss text whose unchanged digits remain on screen.
This is a small test screen, not a general terminal emulator.
"""

import re
import unicodedata


SEQUENCES = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)|\x1b.|[^\x1b]+")


def screen_text(raw):
    rows = {}
    x = y = 0

    def row():
        return rows.setdefault(y, [])

    for match in SEQUENCES.finditer(raw.decode("utf-8", errors="replace")):
        token = match.group()
        if token.startswith("\x1b["):
            params, op = token[2:-1], token[-1]
            if params and any(c not in "0123456789;" for c in params):
                continue
            values = [int(p or 0) for p in params.split(";")]
            n = values[0] or 1
            if op == "A":
                y = max(0, y - n)
            elif op == "B":
                y += n
            elif op == "C":
                x += n
            elif op == "D":
                x = max(0, x - n)
            elif op == "G":
                x = n - 1
            elif op in ("H", "f"):
                y = n - 1
                x = (values[1] or 1) - 1 if len(values) > 1 else 0
            elif op == "K":
                mode = values[0]
                if mode == 0:
                    del row()[x:]
                elif mode == 1:
                    line = row()
                    line[:min(x + 1, len(line))] = [" "] * min(x + 1, len(line))
                elif mode == 2:
                    rows[y] = []
            elif op == "J":
                if values[0] in (2, 3):
                    rows.clear()
                elif values[0] == 0:
                    del row()[x:]
                    rows = {r: line for r, line in rows.items() if r <= y}
            elif op == "P":
                del row()[x:x + n]
            elif op == "@":
                row()[x:x] = [" "] * n
            continue
        if token == "\x1bM":
            y = max(0, y - 1)
            continue
        if token.startswith("\x1b"):
            continue
        for char in token:
            if char == "\r":
                x = 0
            elif char == "\n":
                y += 1
            elif char == "\b":
                x = max(0, x - 1)
            elif char == "\t":
                x = (x // 8 + 1) * 8
            elif char.isprintable():
                if unicodedata.combining(char):
                    continue
                line = row()
                width = 2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
                if len(line) < x + width:
                    line.extend([" "] * (x + width - len(line)))
                line[x] = char
                if width == 2:
                    line[x + 1] = ""
                x += width
    return "\n".join("".join(rows[r]).rstrip() for r in sorted(rows))
