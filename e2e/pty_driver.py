#!/usr/bin/env python3
import base64
import json
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import threading
import time

import fcntl
from vt_screen import screen_text


def parse_args(argv):
    if len(argv) < 4 or argv[0] != "--stdout-file" or argv[2] != "--":
        raise SystemExit("usage: pty_driver.py --stdout-file PATH -- CMD...")
    return argv[1], argv[3:]


def write_msg(obj):
    sys.stdout.write(json.dumps(obj, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def b64(data):
    return base64.b64encode(data).decode("ascii")


def mode_json(mode):
    iflag, oflag, cflag, lflag, ispeed, ospeed, cc = mode
    cc_out = []
    for item in cc:
        if isinstance(item, (bytes, bytearray)):
            cc_out.append(list(item))
        else:
            cc_out.append(item)
    return [iflag, oflag, cflag, lflag, ispeed, ospeed, cc_out]


def set_winsize(fd, rows, cols):
    winsize = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, winsize)


def disable_echo(fd):
    mode = termios.tcgetattr(fd)
    mode[3] = mode[3] & ~termios.ECHO & ~termios.ECHOE & ~termios.ECHOK & ~termios.ECHONL
    termios.tcsetattr(fd, termios.TCSANOW, mode)
    return termios.tcgetattr(fd)


def become_session():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


def kill_group(proc):
    if proc.poll() is not None:
        return
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=2)


def read_stdout(path):
    try:
        with open(path, "rb") as handle:
            return handle.read()
    except OSError:
        return b""


def snapshot(transcript, lock):
    with lock:
        return bytes(transcript)


def await_text(proc, transcript, lock, text, timeout_ms, kill):
    needle = text.encode()
    deadline = time.monotonic() + (timeout_ms / 1000)
    while True:
        raw = snapshot(transcript, lock)
        if needle in raw or text in screen_text(raw):
            return True, raw
        if proc.poll() is not None:
            raw = snapshot(transcript, lock)
            return needle in raw or text in screen_text(raw), raw
        if time.monotonic() >= deadline:
            kill()
            return False, snapshot(transcript, lock)
        select.select([], [], [], 0.05)


def wait_result(proc, transcript, lock, master, baseline, timeout_ms, kill, out):
    deadline = time.monotonic() + (timeout_ms / 1000)
    while proc.poll() is None and time.monotonic() < deadline:
        select.select([], [], [], 0.05)
    if proc.poll() is None:
        kill()
    time.sleep(0.05)
    code = proc.returncode
    if code is None:
        code = -1
    if code < 0:
        code = 128 + abs(code)
    try:
        final = termios.tcgetattr(master)
        equal = mode_json(baseline) == mode_json(final)
        final_mode = mode_json(final)
    except termios.error as exc:
        equal = False
        final_mode = str(exc)
    out.flush()
    out.seek(0)
    stdout = out.read()
    return code, snapshot(transcript, lock), final_mode, equal, stdout


def pump(master, transcript, lock, stop):
    while not stop.is_set():
        readable, _, _ = select.select([master], [], [], 0.05)
        if not readable:
            continue
        try:
            chunk = os.read(master, 65536)
        except OSError:
            return
        if not chunk:
            return
        with lock:
            transcript.extend(chunk)


def main():
    try:
        stdout_path, argv = parse_args(sys.argv[1:])
    except SystemExit as exc:
        write_msg({"op": "ready", "ok": False, "error": str(exc)})
        return 2

    master, slave = pty.openpty()
    out = None
    proc = None
    stop = threading.Event()
    try:
        set_winsize(slave, 24, 80)
        baseline = disable_echo(slave)
        out = open(stdout_path, "w+b", buffering=0)
        proc = subprocess.Popen(
            argv,
            stdin=slave,
            stdout=slave if os.environ.get("YZ_TEST_STDOUT_TTY") == "1" else out,
            stderr=slave,
            preexec_fn=become_session,
            close_fds=True,
        )
        transcript = bytearray()
        lock = threading.Lock()
        threading.Thread(
            target=pump, args=(master, transcript, lock, stop), daemon=True
        ).start()
        write_msg({"op": "ready", "ok": True, "initial_mode": mode_json(baseline)})

        def kill():
            kill_group(proc)

        for line in sys.stdin:
            if not line.strip():
                continue
            cmd = json.loads(line)
            ident = cmd.get("id")
            op = cmd.get("op")
            if op == "send":
                data = cmd.get("data", "")
                raw = data.encode() if isinstance(data, str) else data
                try:
                    os.write(master, raw)
                    write_msg({"id": ident, "ok": True})
                except OSError as exc:
                    write_msg({"id": ident, "ok": False, "error": str(exc)})
            elif op == "resize":
                set_winsize(slave, int(cmd["rows"]), int(cmd["cols"]))
                write_msg({"id": ident, "ok": True})
            elif op == "await":
                ok, raw = await_text(
                    proc,
                    transcript,
                    lock,
                    cmd.get("text", ""),
                    int(cmd.get("timeout_ms", 3000)),
                    kill,
                )
                msg = {"id": ident, "ok": ok, "stderr_b64": b64(raw),
                       "screen_b64": b64(screen_text(raw).encode())}
                if not ok:
                    msg["error"] = "timeout"
                write_msg(msg)
            elif op == "peek":
                write_msg(
                    {
                        "id": ident,
                        "ok": True,
                        "stdout_b64": b64(read_stdout(stdout_path)),
                        "stderr_b64": b64(snapshot(transcript, lock)),
                    }
                )
            elif op == "hangup":
                if proc.poll() is None:
                    try:
                        os.killpg(proc.pid, signal.SIGHUP)
                    except ProcessLookupError:
                        pass
                write_msg({"id": ident, "ok": True})
            elif op == "interrupt":
                try:
                    os.write(master, b"\x03")
                except OSError:
                    pass
                if proc.poll() is None:
                    try:
                        os.killpg(proc.pid, signal.SIGINT)
                    except ProcessLookupError:
                        pass
                write_msg({"id": ident, "ok": True})
            elif op == "wait":
                code, raw, final, equal, stdout = wait_result(
                    proc,
                    transcript,
                    lock,
                    master,
                    baseline,
                    int(cmd.get("timeout_ms", 10000)),
                    kill,
                    out,
                )
                write_msg(
                    {
                        "id": ident,
                        "ok": True,
                        "exit_code": code,
                        "stdout_b64": b64(stdout),
                        "stderr_b64": b64(raw),
                        "final_mode": final,
                        "modes_equal": equal,
                    }
                )
            else:
                write_msg({"id": ident, "ok": False, "error": "unknown op"})
        return 0
    except Exception as exc:
        write_msg({"op": "ready", "ok": False, "error": str(exc)})
        return 1
    finally:
        stop.set()
        if proc is not None:
            kill_group(proc)
        if out is not None:
            out.close()
        os.close(master)
        os.close(slave)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BrokenPipeError:
        raise SystemExit(0)
