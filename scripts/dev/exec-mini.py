#!/usr/bin/env python3
# A minimal executor (docs/exec.md) in plain Python, standard library only, its WebSocket too: a
# developer's test that the protocol is all an outside program needs, not shipped. Each Iridium
# added pairs once (a code to allow on that Iridium's page), then runs its agents here, and its
# command lines too where shell is given (a person's $ on the page, an
# agent's ir sh, by run). One process serves them all and takes config changes
# as they come, so an Iridium is switched on and off without a restart.
#
#   exec-mini.py add <url> [--name N] [--agents pi,claude] [--shell] [--agent-shell]
#   exec-mini.py on|off|rm <url>
#   exec-mini.py ls
#   exec-mini.py run                         stays up: every Iridium switched on
#   exec-mini.py <url> [name] [agent ...] [--shell]   one Iridium, in the foreground
import argparse, base64, codecs, hashlib, json, os, platform, shutil, signal, socket, ssl, struct, subprocess
import re, sys, threading, time, urllib.error, urllib.parse, urllib.request

PROGRAM = "exec-mini.py 0.2"
OS = {"win32": "windows"}.get(sys.platform, sys.platform)
UA = {"user-agent": PROGRAM.replace(" ", "/")}  # a proxy may turn Python's own away (Cloudflare 1010)
ROOT = os.path.join(os.path.expanduser("~"), ".iridium-exec-mini")
CONFIG = os.path.join(ROOT, "config.json")
STATUS = os.path.join(ROOT, "status.json")


def say(m):
    print(time.strftime("%H:%M:%S"), m, flush=True)


def norm(url):
    url = url.strip().rstrip("/")
    if not re.match(r"^https?://[^/]+$", url):
        sys.exit(f"not an Iridium address: {url} (https://host[:port])")
    return url


def key(url):
    """Each Iridium its own TOKEN and holders, under its host and port as ir join names them."""
    return re.sub(r"[^a-z0-9.-]+", "-", urllib.parse.urlsplit(url).netloc.lower()).strip("-.")


def host_name():
    return (socket.gethostname().split(".")[0] or "exec-mini")[:12]


def lock(path):
    """An exclusive lock on path, or None when another process holds it."""
    f = open(path, "w")
    try:
        import fcntl
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except ImportError:
        pass  # Windows: no lock
    except OSError:
        f.close()
        return None
    return f


def load():
    try:
        with open(CONFIG) as f:
            return json.load(f)
    except FileNotFoundError:
        return {"iridiums": {}}


def save(c):
    os.makedirs(ROOT, mode=0o700, exist_ok=True)
    tmp = CONFIG + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(c, f, indent=2)
    os.replace(tmp, CONFIG)


status, status_mu = {}, threading.Lock()


def set_status(url, m):
    with status_mu:
        if m is None:
            status.pop(url, None)
        else:
            status[url] = {"state": m, "at": int(time.time())}
        try:
            tmp = STATUS + ".tmp"
            with open(tmp, "w") as f:
                json.dump(status, f)
            os.replace(tmp, STATUS)
        except OSError:
            pass


class Refused(OSError):
    """The Iridium turns the TOKEN down (401): its app was removed. Any other refusal (403 with a
    reason, a proxy's 5xx) is for now: the TOKEN stays and it connects again."""


class Fail(Exception):
    def __init__(self, code, msg):
        super().__init__(msg)
        self.code = code


# ---- agents here ----

def find_agent(name):
    path = shutil.which(name)
    if not path:
        return None
    real = os.path.realpath(path)
    argv, version = [real], ""
    with open(real, "rb") as f:
        first = f.readline()
    if first.startswith(b"#!") and b"node" in first and shutil.which("node"):
        argv = [shutil.which("node"), real]
    d = os.path.dirname(real)
    while d != os.path.dirname(d):
        try:
            with open(os.path.join(d, "package.json")) as f:
                version = json.load(f).get("version", "")
            break
        except (OSError, ValueError):
            d = os.path.dirname(d)
    return {"agent": name, "argv": argv, "version": version}


# ---- a WebSocket client (RFC 6455), just enough ----

class WS:
    def __init__(self, url, token):
        u = urllib.parse.urlsplit(url)
        port = u.port or (443 if u.scheme == "https" else 80)
        s = socket.create_connection((u.hostname, port), timeout=30)
        if u.scheme == "https":
            s = ssl.create_default_context().wrap_socket(s, server_hostname=u.hostname)
        key = base64.b64encode(os.urandom(16)).decode()
        s.sendall((f"GET /v1/internal/exec HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                   f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nUser-Agent: {UA['user-agent']}\r\nAuthorization: Bearer {token}\r\n\r\n").encode())
        self.r, self.s, self.lock = s.makefile("rb"), s, threading.Lock()
        status = self.r.readline()
        while self.r.readline() not in (b"\r\n", b""):
            pass
        if b" 401 " in status:
            raise Refused(status.decode().strip())
        if b" 101 " not in status:
            raise OSError(f"no upgrade: {status.decode().strip()}")
        s.settimeout(None)

    def send(self, obj, op=1):
        data = obj if isinstance(obj, bytes) else json.dumps(obj).encode()
        n, mask = len(data), os.urandom(4)
        head = bytes([0x80 | op]) + (bytes([0x80 | n]) if n < 126 else
                                     bytes([0xFE]) + struct.pack(">H", n) if n < 65536 else
                                     bytes([0xFF]) + struct.pack(">Q", n))
        with self.lock:
            self.s.sendall(head + mask + _mask(data, mask))

    def recv(self):
        """The next text message; None once closed. Pings are answered here."""
        msg = b""
        while True:
            h = self.r.read(2)
            if len(h) < 2:
                return None
            fin, op, n = h[0] & 0x80, h[0] & 0x0F, h[1] & 0x7F
            if n == 126:
                n = struct.unpack(">H", self.r.read(2))[0]
            elif n == 127:
                n = struct.unpack(">Q", self.r.read(8))[0]
            mask = self.r.read(4) if h[1] & 0x80 else None
            data = self.r.read(n)
            if mask:
                data = _mask(data, mask)
            if op == 8:
                return None
            if op == 9:
                self.send(data, op=10)
            elif op in (0, 1, 2):
                msg += data
                if fin:
                    return msg.decode()

    def close(self):
        # shutdown first: it wakes a reader blocked in another thread, close alone does not.
        for f in (lambda: self.s.shutdown(socket.SHUT_RDWR), self.s.close):
            try:
                f()
            except OSError:
                pass


def _mask(data, mask):
    n = len(data)
    return (int.from_bytes(data, "big") ^ int.from_bytes((mask * (n // 4 + 1))[:n], "big")).to_bytes(n, "big")


# ---- holders: an agent process, its out.log, its exit ----

class Hold:
    def __init__(self, link, hid):
        self.id, self.log, self.dir = hid, link.log, os.path.join(link.state, "holds", hid)
        self.proc, self.in_seq, self.exit, self.gen = None, 0, None, 0
        self.ended, self.mu = threading.Event(), threading.Lock()

    def out(self):
        return os.path.join(self.dir, "out.log")

    def state(self):
        size = os.path.getsize(self.out()) if os.path.exists(self.out()) else 0
        return {"hold": self.id, "outSize": size, "inSeq": self.in_seq, "exit": self.exit}

    def start(self, p):
        os.makedirs(self.dir, mode=0o700, exist_ok=True)
        out, err = open(self.out(), "ab"), open(os.path.join(self.dir, "err.log"), "ab")
        self.grace = (p.get("stopGraceMs") or 5000) / 1000
        env = dict(kv.split("=", 1) for kv in p.get("env") or [] if "=" in kv)
        try:
            self.proc = subprocess.Popen(p["argv"], env=env, cwd=p.get("cwd") or None, stdin=subprocess.PIPE,
                                         stdout=out, stderr=err, start_new_session=os.name != "nt")
        except OSError as e:
            err.write(f"cannot start {p['argv'][0]}: {e}\n".encode())
            self.finish({"from": "agent", "code": 127, "signal": None})
            return {"pid": 0}
        finally:
            out.close()
            err.close()
        threading.Thread(target=self.wait, daemon=True).start()
        self.log(f"{self.id}: started pid {self.proc.pid}: {' '.join(p['argv'])[:120]}")
        return {"pid": self.proc.pid}

    def wait(self):
        rc = self.proc.wait()
        sig = signal.Signals(-rc).name[3:] if rc < 0 else None
        self.finish({"from": "agent", "code": None if sig else rc, "signal": sig})

    def finish(self, x):
        self.log(f"{self.id}: ended {x}")
        self.exit = x
        self.ended.set()

    def write(self, p):
        with self.mu:
            if p["seq"] > self.in_seq and self.proc and self.proc.poll() is None:
                self.proc.stdin.write(p["line"].encode() + b"\n")
                self.proc.stdin.flush()
                self.in_seq = p["seq"]
        return {"outSize": self.state()["outSize"]}

    def tail(self, ws, at):
        self.gen += 1
        gen = self.gen

        def run():
            while gen == self.gen:
                ended = self.ended.is_set()
                data = b""
                if os.path.exists(self.out()):
                    with open(self.out(), "rb") as f:
                        f.seek(at_[0])
                        data = f.read(1 << 20)
                    i = data.rfind(b"\n")
                    data = data[:i + 1] if i >= 0 else (data if ended else b"")
                try:
                    if data:
                        ws.send({"event": "out", "params": {"hold": self.id, "off": at_[0], "data": data.decode("utf-8", "replace")}})
                        at_[0] += len(data)
                        continue
                    if ended:
                        tail = open(os.path.join(self.dir, "err.log"), "rb").read()[-2048:].decode("utf-8", "replace")
                        ws.send({"event": "exit", "params": {"hold": self.id, **self.exit, "errTail": tail}})
                        return
                except OSError:
                    return
                time.sleep(0.05)

        at_ = [at]
        threading.Thread(target=run, daemon=True).start()
        return {}

    def stop(self, hard):
        if self.proc and self.proc.poll() is None:
            if not hard:
                try:
                    self.proc.stdin.close()
                    self.proc.wait(self.grace)
                    return {}
                except (OSError, subprocess.TimeoutExpired):
                    pass
            try:
                os.killpg(self.proc.pid, signal.SIGKILL) if os.name != "nt" else self.proc.kill()
            except OSError:
                pass
        return {}


# ---- run: a command line now, its output pushed as it comes (with --shell) ----

def own_env():
    """Its environment without what points an ir at an Iridium here (that one sets its own)."""
    return {k: v for k, v in os.environ.items() if not re.fullmatch(r"IRIDIUM_(SESSION|SOCKET|CONFIG|URL|TOKEN)", k)}


def machine_id():
    """A hash of this computer's ID, as internal/machineid gives it: one equal to the Iridium's own
    is an app on its host, whose agents are not taken."""
    raw = os.environ.get("IRIDIUM_MACHINE_ID")
    if raw is None:
        for p in ("/etc/machine-id", "/var/lib/dbus/machine-id"):
            try:
                raw = open(p).read()
                break
            except OSError:
                pass
    if raw is None and sys.platform == "darwin":
        out = subprocess.run(["ioreg", "-rd1", "-c", "IOPlatformExpertDevice"], capture_output=True, text=True).stdout
        m = re.search(r'"IOPlatformUUID" = "([^"]+)"', out)
        raw = m.group(1) if m else None
    raw = (raw or "").strip()
    if not raw or raw == "none":
        return ""
    return hashlib.sha256(b"iridium machine " + raw.encode()).hexdigest()[:32]


class Run:
    def __init__(self, link, ws, rid):
        self.link, self.ws, self.id, self.proc, self.stopping = link, ws, rid, None, False

    @staticmethod
    def start(link, ws, p):
        runs = link.runs
        if not link.shell:
            raise Fail("denied", "command lines do not run here")
        rid, line = p.get("run", ""), p.get("line", "")
        if not rid or not line:
            raise Fail("bad_request", "bad run")
        if rid in runs:
            raise Fail("exists", rid)
        r = runs[rid] = Run(link, ws, rid)
        env = dict(kv.split("=", 1) for kv in p["env"] if "=" in kv) if p.get("env") else own_env()
        argv = ["cmd.exe", "/d", "/s", "/c", line] if os.name == "nt" else ["/bin/sh", "-c", line]
        try:
            r.proc = subprocess.Popen(argv, env=env, cwd=p.get("cwd") or os.path.expanduser("~"), stdin=subprocess.DEVNULL,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=os.name != "nt")
        except OSError as e:
            runs.pop(rid, None)
            threading.Thread(target=r.send, args=("run.exit", {"code": 127, "error": str(e)}), daemon=True).start()
            return {}
        pumps = [threading.Thread(target=r.pump, args=(f, err), daemon=True) for f, err in ((r.proc.stdout, False), (r.proc.stderr, True))]
        for t in pumps:
            t.start()
        threading.Thread(target=r.wait, args=(pumps,), daemon=True).start()
        link.log(f"{rid}: running a command line")
        return {}

    def send(self, event, params):
        try:
            self.ws.send({"event": event, "params": {"run": self.id, **params}})
        except OSError:
            pass

    def pump(self, f, err):
        dec = codecs.getincrementaldecoder("utf-8")("replace")  # whole characters each
        while chunk := f.read1(32 << 10):
            if text := dec.decode(chunk):
                self.send("run.out", {"data": text, **({"err": True} if err else {})})
        if text := dec.decode(b"", final=True):
            self.send("run.out", {"data": text, **({"err": True} if err else {})})

    def wait(self, pumps):
        rc = self.proc.wait()
        for t in pumps:
            t.join(2)
        if any(t.is_alive() for t in pumps):
            self.kill()  # what it left in the background holds its output
            for t in pumps:
                t.join(1)
        self.link.runs.pop(self.id, None)
        sig = signal.Signals(-rc).name[3:] if rc < 0 else None
        self.send("run.exit", {"code": None if sig else rc, "signal": sig})

    def signal_tree(self, sig):
        try:
            os.killpg(self.proc.pid, sig) if os.name != "nt" else self.proc.kill()
        except OSError:
            pass

    def kill(self):
        self.signal_tree(signal.SIGKILL if os.name != "nt" else None)

    def stop(self):
        if self.stopping:
            return {}
        self.stopping = True
        self.signal_tree(signal.SIGTERM if os.name != "nt" else None)

        def later():
            time.sleep(2)
            if self.proc.poll() is None:
                self.kill()

        threading.Thread(target=later, daemon=True).start()
        return {}


# ---- files ----

def allowed(path):
    """Never given: the config, any Iridium's TOKEN, ~/.ssh. Scratch and holders' logs are."""
    real, root = os.path.realpath(path), os.path.realpath(ROOT)
    ssh = os.path.realpath(os.path.expanduser("~/.ssh"))
    if (real == ssh or real.startswith(ssh + os.sep) or real == root or
            real.startswith(root + os.sep) and (os.path.dirname(real) == root or os.path.basename(real) in ("token", "lock"))):
        raise Fail("denied", path)
    return path


def stat(path, head=0):
    st = os.stat(allowed(path))
    kind = "link" if os.path.islink(path) else "dir" if os.path.isdir(path) else "file" if os.path.isfile(path) else "other"
    out = {"size": st.st_size, "mtime": int(st.st_mtime * 1000), "type": kind}
    if head and kind == "file":
        with open(path, "rb") as f:
            out["head"] = hashlib.sha256(f.read(head)).hexdigest()
    return out


def fs(method, p):
    path = p["path"]
    if method == "fs.stat":
        return stat(path, p.get("head", 0))
    if method == "fs.list":
        out = []
        for e in os.scandir(allowed(path)):
            try:
                out.append({"name": e.name, **stat(e.path)})
            except (OSError, Fail):
                pass  # gone, or not given
        return {"entries": out}
    if method == "fs.read":
        with open(allowed(path), "rb") as f:
            f.seek(p.get("offset", 0))
            return {"data": base64.b64encode(f.read(min(p.get("length", 4 << 20), 4 << 20))).decode()}
    if method == "fs.write":
        data = base64.b64decode(p["data"])
        if hashlib.sha256(data).hexdigest() != p["sha256"]:
            raise Fail("bad_request", "sha256 does not match")
        if os.path.exists(allowed(path)) and not p.get("replace"):
            raise Fail("exists", path)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = f"{path}.{time.time_ns()}.tmp"
        with open(tmp, "wb") as f:
            f.write(data)
        os.replace(tmp, path)
        return {}
    if method == "fs.mkdir":
        os.makedirs(allowed(path), exist_ok=True)
        return {}
    raise Fail("unsupported", method)


# ---- one Iridium ----

class Link:
    def __init__(self, url, cfg):
        self.url, self.key = url, key(url)
        self.name = (cfg.get("name") or host_name())[:12]
        self.agent_names = cfg.get("agents", ["pi"])
        self.shell = bool(cfg.get("shell"))
        self.agent_shell = self.shell and bool(cfg.get("agentShell"))
        self.state = os.path.join(ROOT, self.key)
        self.holds, self.runs, self.ws, self.stopped = {}, {}, None, threading.Event()

    def log(self, m):
        say(f"[{self.key}] {m}")

    def note(self, m):
        self.log(m)
        set_status(self.url, m)

    def agents(self):
        return [a for a in map(find_agent, self.agent_names) if a]

    def stop(self):
        """Off: no more connecting, the connection closed, its processes ended (by run)."""
        self.stopped.set()
        if self.ws:
            self.ws.close()

    def handle(self, ws, method, p):
        if method.startswith("fs."):
            return fs(method, p)
        if method == "run":
            return Run.start(self, ws, p)
        if method == "run.stop":
            r = self.runs.get(p.get("run", ""))
            return r.stop() if r else {}
        if method == "hold.start":
            h = self.holds.get(p["hold"])
            if h:
                return {"pid": h.proc.pid if h.proc else 0}
            argv = p.get("argv") or []
            if not self.shell and not any(a["argv"] == argv[:len(a["argv"])] for a in self.agents()):
                raise Fail("denied", "only its agents start here")
            h = self.holds[p["hold"]] = Hold(self, p["hold"])
            return h.start(p)
        h = self.holds.get(p.get("hold", ""))
        if not h:
            raise Fail("not_found", p.get("hold", ""))
        if method == "hold.write":
            return h.write(p)
        if method == "hold.tail":
            return h.tail(ws, p.get("from", 0))
        if method in ("hold.stop", "hold.kill"):
            self.log(f"{h.id}: {method}")
            return h.stop(method == "hold.kill")
        if method == "hold.forget":
            self.holds.pop(h.id, None)
            shutil.rmtree(h.dir, ignore_errors=True)
            return {}
        raise Fail("unsupported", method)

    def answer(self, ws, f):
        try:
            out = {"id": f["id"], "result": self.handle(ws, f["method"], f.get("params") or {})}
        except Fail as e:
            out = {"id": f["id"], "error": {"code": e.code, "message": str(e)}}
        except FileNotFoundError as e:
            out = {"id": f["id"], "error": {"code": "not_found", "message": str(e)}}
        except PermissionError as e:
            out = {"id": f["id"], "error": {"code": "denied", "message": str(e)}}
        except Exception as e:  # noqa: BLE001: the Iridium hears every failure
            out = {"id": f["id"], "error": {"code": "failed", "message": f"{type(e).__name__}: {e}"}}
        try:
            ws.send(out)
        except OSError:
            pass

    def serve(self, token):
        ws = self.ws = WS(self.url, token)
        if self.stopped.is_set():
            ws.close()
            return
        scratch = os.path.join(self.state, "scratch")
        os.makedirs(scratch, exist_ok=True)
        ws.send({"id": 1, "method": "hello", "params": {
            "protocol": 1, "program": PROGRAM, "os": OS,
            "arch": platform.machine(), "home": os.path.expanduser("~"), "sep": os.sep, "scratch": scratch,
            "env": [f"{k}={v}" for k, v in own_env().items()], "agents": self.agents(),
            "holds": [h.state() for h in self.holds.values()],
            # shell, and the older two for an older Iridium; run: ir sh streamed, not as a holder.
            "shell": self.shell, "noShell": not self.shell, "agentShell": self.agent_shell,
            "caps": ["run"], "machineId": machine_id()}})
        alive = [True]

        def ping():
            while alive[0]:
                time.sleep(25)
                try:
                    ws.send({"event": "ping"})
                except OSError:
                    return

        threading.Thread(target=ping, daemon=True).start()
        try:
            while (m := ws.recv()) is not None:
                f = json.loads(m)
                if f.get("id") == 1 and "method" not in f:
                    self.note(f"connected as {(f.get('result') or {}).get('name')}" if "result" in f else f"hello: {f.get('error')}")
                elif f.get("method"):
                    # stop waits for its grace: off the reader; the rest in order.
                    if f["method"] == "hold.stop":
                        threading.Thread(target=self.answer, args=(ws, f), daemon=True).start()
                    else:
                        self.answer(ws, f)
        except (OSError, ValueError):
            if not self.stopped.is_set():
                raise
        finally:
            alive[0] = False
            ws.close()
            for r in [r for r in self.runs.values() if r.ws is ws]:
                r.kill()  # the connection that asked for it is gone

    def pair(self):
        """A TOKEN once allowed on the Iridium's page; None when declined or switched off."""
        body = json.dumps({"name": self.name, "os": OS, "arch": platform.machine(), "program": PROGRAM,
                           "shell": self.shell, "agents": [{"agent": a["agent"], "version": a["version"]} for a in self.agents()],
                           "noShell": not self.shell, "agentShell": self.agent_shell}).encode()
        while not self.stopped.is_set():
            req = urllib.request.Request(self.url + "/v1/internal/pair", data=body, headers={"content-type": "application/json", **UA})
            try:
                r = json.load(urllib.request.urlopen(req, timeout=30))
            except urllib.error.HTTPError as e:
                if e.code != 403:
                    raise
                self.note("pairing is closed there (ir apps open, or 新建应用): waiting")  # pair_closed, too_many_pending
                if self.stopped.wait(5):
                    return None
                continue
            self.note(f'pairing code {r["code"]}: allow "{self.name}" on the Iridium page')
            while not self.stopped.wait(2):
                try:
                    st = json.load(urllib.request.urlopen(urllib.request.Request(self.url + "/v1/internal/pair/" + r["request"], headers=UA), timeout=30))
                except urllib.error.HTTPError as e:
                    st = {"status": "expired" if e.code in (404, 422) else "pending"}
                except (OSError, ValueError):
                    st = {"status": "pending"}
                if st["status"] == "allowed" and st.get("token"):
                    return st["token"]
                if st["status"] == "denied":
                    self.note("declined")
                    return None
                if st["status"] == "expired":
                    break
        return None

    def run(self):
        os.makedirs(self.state, mode=0o700, exist_ok=True)
        # One run per Iridium: a second would take the same TOKEN and the two would push each other off.
        held = lock(os.path.join(self.state, "lock"))
        while not held:
            self.note("another exec-mini serves this Iridium")
            if self.stopped.wait(10):
                return
            held = lock(os.path.join(self.state, "lock"))
        tf = os.path.join(self.state, "token")
        self.log(f"agents: {', '.join(a['agent'] + ' ' + a['version'] for a in self.agents()) or 'none'}; "
                 f"command lines: {'people and agents' if self.agent_shell else 'people' if self.shell else 'no'}")
        backoff = 1
        try:
            while not self.stopped.is_set():
                began = time.time()
                try:
                    if not os.path.exists(tf):
                        token = self.pair()
                        if token is None:
                            return
                        fd = os.open(tf, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
                        os.write(fd, token.encode())
                        os.close(fd)
                        self.note("paired")
                    self.serve(open(tf).read().strip())
                    if not self.stopped.is_set():
                        self.note("disconnected")
                except Refused as e:
                    self.note(f"the Iridium no longer takes this machine ({e}): pairing again")
                    os.remove(tf)
                    continue
                except (OSError, ValueError, KeyError) as e:
                    self.note(f"connect: {e}")
                if time.time() - began > 60:
                    backoff = 1
                if self.stopped.wait(backoff):
                    break
                backoff = min(backoff * 2, 30)
        finally:
            for h in list(self.holds.values()):
                h.stop(True)
            held.close()


# ---- commands ----

def daemon():
    os.makedirs(ROOT, mode=0o700, exist_ok=True)
    held = lock(os.path.join(ROOT, "run.lock"))
    if not held:
        sys.exit("already running")
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    links = {}  # url -> (its config, Link, thread)
    try:
        while True:
            try:
                want = {u: c for u, c in load().get("iridiums", {}).items() if c.get("on", True)}
            except (OSError, ValueError) as e:
                say(f"config: {e}")
                want = {u: c for u, (c, _, _) in links.items()}
            for u in list(links):
                c, l, t = links[u]
                if want.get(u) != c:
                    l.log("off" if u not in want else "config changed")
                    l.stop()
                    t.join(15)
                    del links[u]
                    set_status(u, None)
            for u, c in want.items():
                if u not in links:
                    l = Link(u, c)
                    t = threading.Thread(target=l.run, daemon=True)
                    t.start()
                    links[u] = (c, l, t)
            time.sleep(2)
    finally:
        for _, l, t in links.values():
            l.stop()
        for _, l, t in links.values():
            t.join(15)
        with status_mu:
            status.clear()
        set_status("", None)


def ls():
    c = load().get("iridiums", {})
    running = lock(os.path.join(ROOT, "run.lock")) if os.path.isdir(ROOT) else None
    up = running is None and os.path.isdir(ROOT)
    if running:
        running.close()
    try:
        st = json.load(open(STATUS)) if up else {}
    except (OSError, ValueError):
        st = {}
    print(f"exec-mini run: {'running' if up else 'not running'}")
    for u, e in c.items():
        shell = "commands (agents too)" if e.get("shell") and e.get("agentShell") else "commands" if e.get("shell") else "agents only"
        paired = "paired" if os.path.exists(os.path.join(ROOT, key(u), "token")) else "not paired"
        now = st.get(u, {}).get("state", "") if e.get("on", True) else ""
        print(f"{u}  {'on ' if e.get('on', True) else 'off'}  {e.get('name') or host_name()}  "
              f"agents={','.join(e.get('agents', ['pi'])) or '-'}  {shell}  {paired}  {now}".rstrip())


def main():
    if len(sys.argv) > 1 and re.match(r"^https?://", sys.argv[1]):
        argv = [a for a in sys.argv[1:] if a != "--shell"]
        l = Link(norm(argv[0]), {"name": argv[1] if len(argv) > 1 else "exec-mini", "agents": argv[2:] or ["pi"],
                                 "shell": "--shell" in sys.argv[1:], "agentShell": "--shell" in sys.argv[1:]})
        try:
            l.run()
        except KeyboardInterrupt:
            l.stop()
        return
    ap = argparse.ArgumentParser(prog="exec-mini.py")
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("add", help="add an Iridium, or change one, and switch it on")
    a.add_argument("url")
    a.add_argument("--name", help="the machine's name there (12 characters at most)")
    a.add_argument("--agents", help="comma-separated, default pi; '' for none")
    a.add_argument("--shell", action="store_true", help="its people may run command lines here")
    a.add_argument("--agent-shell", action="store_true", help="its agents too (ir sh); implies --shell")
    for c in ("on", "off", "rm"):
        sub.add_parser(c).add_argument("url")
    sub.add_parser("ls")
    sub.add_parser("run")
    args = ap.parse_args()
    if args.cmd == "run":
        return daemon()
    if args.cmd == "ls":
        return ls()
    url, c = norm(args.url), load()
    ir = c.setdefault("iridiums", {})
    if args.cmd == "add":
        e = ir.setdefault(url, {})
        if args.name is not None:
            e["name"] = args.name[:12]
        if args.agents is not None:
            e["agents"] = [x for x in args.agents.split(",") if x.strip()]
        e["shell"] = args.shell or args.agent_shell
        e["agentShell"] = args.agent_shell
        e["on"], e["at"] = True, int(time.time())
    elif url not in ir:
        sys.exit(f"not added: {url}")
    elif args.cmd == "rm":
        del ir[url]  # its TOKEN stays: added again, no pairing
    else:
        ir[url]["on"], ir[url]["at"] = args.cmd == "on", int(time.time())
    save(c)
    ls()


if __name__ == "__main__":
    main()
