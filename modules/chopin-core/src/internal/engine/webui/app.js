(function () {
  "use strict";

  var TOKEN_KEY = "chopin-pilot.webui.token";
  var SESSION_KEY = "chopin-pilot.webui.session";
  var CLAIM_TAG = "webui";

  var S = {
    token: "",
    session: null,
    sessionActive: false,
    sessions: [],
    holder: null,
    displayID: 0,
    claiming: false,
    mode: "none",
    snapshot: false,
    snapshotURL: "",
    snapshotBusy: false,
    snapshotTimer: null,
    streamTimer: null,
    refreshTimer: null,
    heartbeatTimer: null,
    pc: null,
    viewerID: "",
    pendingIce: [],
    width: 0,
    height: 0,
    pointerId: null,
    gesture: null,
    longTimer: null,
    bannerError: ""
  };

  function $(id) { return document.getElementById(id); }
  var loginView = $("loginView");
  var appView = $("appView");
  var tokenInput = $("tokenInput");
  var loginBtn = $("loginBtn");
  var loginError = $("loginError");
  var versionEl = $("version");
  var statusBadge = $("statusBadge");
  var displaySelect = $("displaySelect");
  var logoutBtn = $("logoutBtn");
  var banner = $("banner");
  var bannerText = $("bannerText");
  var retryStreamBtn = $("retryStreamBtn");
  var stage = $("stage");
  var video = $("video");
  var snapshot = $("snapshot");
  var stageHint = $("stageHint");
  var sessionInfo = $("sessionInfo");
  var claimBtn = $("claimBtn");
  var releaseBtn = $("releaseBtn");
  var textInput = $("textInput");
  var sendTextBtn = $("sendTextBtn");
  var clearTextBtn = $("clearTextBtn");
  var inputHint = $("inputHint");
  var keyButtons = Array.prototype.slice.call(document.querySelectorAll("button.key"));
  var activity = $("activity");
  var activityWhat = $("activityWhat");
  var activityMeta = $("activityMeta");
  var dDaemon = $("dDaemon");
  var dA11y = $("dA11y");
  var dBg = $("dBg");
  var dSessions = $("dSessions");
  var dUptime = $("dUptime");
  var accessUrl = $("accessUrl");
  var copyUrlBtn = $("copyUrlBtn");

  function storageGet(key) {
    try { return window.sessionStorage.getItem(key) || ""; } catch (e) { return ""; }
  }
  function storageSet(key, value) {
    try { window.sessionStorage.setItem(key, value); } catch (e) { /* memory-only */ }
  }
  function storageRemove(key) {
    try { window.sessionStorage.removeItem(key); } catch (e) { /* ignore */ }
  }

  function errorText(e) {
    if (!e) return window.t("unknown");
    if (e.code === "unauthorized") return window.t("tokenInvalid");
    // These are the codes the daemon actually emits (see internal/httpx).
    if (e.code === "conflict") return window.t("sessionConflict");
    if (e.code === "not_ready") return window.t("hudUnavailable");
    if (e.code === "unavailable" && e.details && e.details.indexOf("viewer limit") >= 0) {
      return window.t("viewerLimit");
    }
    return e.message || window.t("unknown");
  }

  async function api(path, opts) {
    opts = opts || {};
    var headers = {
      "Authorization": "Bearer " + S.token,
      "X-Chopin-Client": "webui"
    };
    if (opts.session !== false && S.session && S.session.token) {
      headers["X-Chopin-Session"] = S.session.token;
    }
    var body = opts.body;
    if (body && typeof body !== "string" && !(body instanceof Blob)) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(body);
    }
    var res = await fetch(path, {
      method: opts.method || "GET",
      headers: headers,
      body: body,
      cache: "no-store"
    });
    if (res.status === 401) {
      var unauthorized = new Error("unauthorized");
      unauthorized.code = "unauthorized";
      throw unauthorized;
    }
    var ct = res.headers.get("content-type") || "";
    if (ct.indexOf("application/json") >= 0) {
      var payload = await res.json();
      // The API has ONE envelope and it is the error one: a successful response
      // is the payload itself, a failure is {"error":{code,message,detail}}.
      // Treating every success as {"ok":true,"data":...} made every call throw.
      if (payload && payload.error) {
        var err = new Error(payload.error.message || "request failed");
        err.code = payload.error.code;
        err.details = payload.error.detail;
        throw err;
      }
      if (!res.ok) {
        var httpErr = new Error("HTTP " + res.status);
        httpErr.code = "http_" + res.status;
        throw httpErr;
      }
      return payload;
    }
    if (!res.ok) {
      var httpErr = new Error("HTTP " + res.status);
      httpErr.code = "http_" + res.status;
      throw httpErr;
    }
    return res;
  }

  function showLogin(message) {
    stopTimers();
    stopStream();
    appView.hidden = true;
    loginView.hidden = false;
    loginError.textContent = message || "";
    if (message) loginError.setAttribute("role", "alert");
  }

  function showApp() {
    loginView.hidden = true;
    appView.hidden = false;
  }

  async function start() {
    showApp();
    setBadge("connecting");
    // Render once up front so the badge shows the real state (free / read-only /
    // active) instead of staying on "connecting" until something else changes.
    render();
    try {
      var health = await fetch("/v1/health", { cache: "no-store" }).then(function (r) { return r.json(); });
      // /v1/health is a flat object; older builds wrapped it in {ok,data}.
      var info = (health && health.data) ? health.data : health;
      if (info && info.version) {
        versionEl.textContent = window.t("version") + " " + info.version;
      }
    } catch (e) { /* health is optional */ }
    await loadDisplays();
    await loadSessions();
    await connectStream();
    startTimers();
    render();
  }

  async function login() {
    var raw = tokenInput.value.trim();
    if (!raw) return;
    if (raw.charAt(0) === "{") {
      try {
        var parsed = JSON.parse(raw);
        raw = parsed.api_token || parsed.token || "";
      } catch (e) {
        loginError.textContent = window.t("tokenInvalid");
        return;
      }
    }
    if (!raw) {
      loginError.textContent = window.t("tokenInvalid");
      return;
    }
    S.token = raw;
    S.bannerError = "";
    loginError.textContent = "";
    storageSet(TOKEN_KEY, raw);
    loginBtn.disabled = true;
    try {
      await start();
    } catch (e) {
      S.token = "";
      storageRemove(TOKEN_KEY);
      showLogin(errorText(e));
    } finally {
      loginBtn.disabled = false;
    }
  }

  async function logout() {
    await releaseSession(true);
    stopTimers();
    stopStream();
    S.token = "";
    S.session = null;
    S.sessionActive = false;
    S.sessions = [];
    S.holder = null;
    storageRemove(TOKEN_KEY);
    storageRemove(SESSION_KEY);
    tokenInput.value = "";
    showLogin("");
  }

  async function loadDisplays() {
    try {
      var data = await api("/v1/displays", { session: false });
      var list = data && Array.isArray(data.displays) ? data.displays : (Array.isArray(data) ? data : []);
      var seen = {};
      displaySelect.innerHTML = "";
      function add(id, label) {
        if (seen[id]) return;
        seen[id] = true;
        var option = document.createElement("option");
        option.value = String(id);
        option.textContent = label;
        displaySelect.appendChild(option);
      }
      add(0, window.t("main"));
      list.forEach(function (d) {
        var id = Number(d && (d.id !== undefined ? d.id : d.display_id));
        if (id > 0) add(id, window.t("virtual") + " " + id);
      });
      if (!seen[S.displayID]) S.displayID = 0;
      displaySelect.value = String(S.displayID);
    } catch (e) {
      if (e.code === "unauthorized") throw e;
    }
  }

  async function loadSessions() {
    var data = await api("/v1/sessions", { session: false });
    S.sessions = (data && data.sessions) || [];
    if (S.session) {
      var mine = S.sessions.filter(function (s) { return s.id === S.session.id; })[0];
      if (!mine) {
        S.session = null;
        S.sessionActive = false;
        storageRemove(SESSION_KEY);
        S.bannerError = window.t("heartbeatLost");
      } else {
        S.sessionActive = true;
      }
    }
    S.holder = S.sessions.filter(function (s) {
      return s.display === S.displayID && (!S.session || s.id !== S.session.id);
    })[0] || null;
    render();
  }

  async function claim() {
    if (!S.token || S.claiming || S.sessionActive) return;
    S.claiming = true;
    S.bannerError = "";
    render();
    try {
      var data = await api("/v1/sessions", {
        method: "POST",
        session: false,
        body: { tag: CLAIM_TAG, display_id: S.displayID, keep_screen_on: true }
      });
      S.session = { id: data.session_id, token: data.session_token, display: data.display_id };
      S.sessionActive = true;
      storageSet(SESSION_KEY, JSON.stringify(S.session));
      await loadSessions();
    } catch (e) {
      if (e.code === "conflict") S.bannerError = window.t("sessionConflict");
      else if (e.code === "not_ready") S.bannerError = window.t("hudUnavailable");
      else S.bannerError = errorText(e);
    } finally {
      S.claiming = false;
      render();
    }
  }

  async function releaseSession(force) {
    if (!S.session) {
      if (!force) render();
      return;
    }
    var session = S.session;
    S.session = null;
    S.sessionActive = false;
    S.holder = null;
    storageRemove(SESSION_KEY);
    try {
      await fetch("/v1/sessions/" + encodeURIComponent(session.id), {
        method: "DELETE",
        headers: {
          "Authorization": "Bearer " + S.token,
          "X-Chopin-Session": session.token,
          "X-Chopin-Client": "webui"
        },
        cache: "no-store"
      });
    } catch (e) { /* reaper will clean it */ }
    if (!force) {
      try { await loadSessions(); } catch (e) { /* keep UI usable */ }
    }
    render();
  }

  function startTimers() {
    stopTimers();
    S.refreshTimer = setInterval(function () {
      loadSessions().catch(function (e) {
        if (e.code === "unauthorized") showLogin(window.t("tokenInvalid"));
      });
    }, 5000);
    S.heartbeatTimer = setInterval(heartbeat, 60000);
    // Two extra pollers rather than one: the rail is cheap and the activity line
    // wants to be close to real time, so they get different periods.
    renderAccess();
    wireCopy();
    pollStatus();
    setInterval(pollStatus, 5000);
    pollActivity();
    setInterval(pollActivity, 1500);
  }

  function stopTimers() {
    if (S.refreshTimer) { clearInterval(S.refreshTimer); S.refreshTimer = null; }
    if (S.heartbeatTimer) { clearInterval(S.heartbeatTimer); S.heartbeatTimer = null; }
  }

  async function heartbeat() {
    if (!S.session) return;
    try {
      await api("/v1/sessions/" + encodeURIComponent(S.session.id) + "/heartbeat", { method: "POST" });
      S.sessionActive = true;
    } catch (e) {
      S.session = null;
      S.sessionActive = false;
      storageRemove(SESSION_KEY);
      S.bannerError = window.t("heartbeatLost");
      render();
    }
  }

  function releaseBeacon() {
    if (!S.session) return;
    try {
      fetch("/v1/sessions/" + encodeURIComponent(S.session.id), {
        method: "DELETE",
        headers: {
          "Authorization": "Bearer " + S.token,
          "X-Chopin-Session": S.session.token,
          "X-Chopin-Client": "webui"
        },
        keepalive: true
      });
    } catch (e) { /* ignore */ }
  }

  async function connectStream() {
    stopStream();
    S.bannerError = "";
    S.snapshot = false;
    render();
    try {
      await connectWebRTC();
      render();
    } catch (e) {
      if (e.code === "unauthorized") {
        showLogin(window.t("tokenInvalid"));
        return;
      }
      if (e.code === "forbidden_scope") {
        S.bannerError = errorText(e);
        render();
        return;
      }
      startSnapshot(e && (e.code === "conflict" || e.code === "unavailable")
        ? errorText(e) : "");
    }
  }

  async function connectWebRTC() {
    if (!window.RTCPeerConnection) throw new Error("WebRTC unsupported");
    var pc = new RTCPeerConnection({ iceServers: [] });
    S.pc = pc;
    S.viewerID = "";
    S.pendingIce = [];
    pc.addTransceiver("video", { direction: "recvonly" });
    pc.ontrack = function (event) {
      var stream = event.streams && event.streams[0];
      if (!stream) return;
      video.srcObject = stream;
      video.hidden = false;
      snapshot.hidden = true;
      video.play().catch(function () {});
    };
    pc.onicecandidate = function (event) {
      if (!event.candidate) return;
      if (S.viewerID) sendIce(event.candidate);
      else S.pendingIce.push(event.candidate);
    };
    pc.onconnectionstatechange = function () {
      if (S.pc !== pc) return;
      if (pc.connectionState === "failed" || pc.connectionState === "disconnected") {
        if (!S.snapshot) startSnapshot("");
      }
    };
    var offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    var data = await api("/v1/stream/webrtc/offer", {
      method: "POST",
      session: false,
      body: { type: "offer", display_id: S.displayID, sdp: offer.sdp }
    });
    S.viewerID = data.viewer_id;
    if (data.width) S.width = data.width;
    if (data.height) S.height = data.height;
    await pc.setRemoteDescription({ type: "answer", sdp: data.sdp });
    var queued = S.pendingIce.slice();
    S.pendingIce = [];
    queued.forEach(sendIce);
    S.mode = "webrtc";
    S.snapshot = false;
    if (S.streamTimer) clearTimeout(S.streamTimer);
    S.streamTimer = setTimeout(function () {
      if (S.mode === "webrtc" && !S.snapshot && video.readyState < 2) startSnapshot("");
    }, 6000);
  }

  function sendIce(candidate) {
    if (!S.viewerID) return;
    api("/v1/stream/webrtc/ice", {
      method: "POST",
      session: false,
      body: {
        viewer_id: S.viewerID,
        candidate: candidate.candidate,
        sdp_mid: candidate.sdpMid || "",
        sdp_mline_index: candidate.sdpMLineIndex || 0
      }
    }).catch(function () {});
  }

  function stopStream() {
    if (S.snapshotTimer) { clearInterval(S.snapshotTimer); S.snapshotTimer = null; }
    if (S.streamTimer) { clearTimeout(S.streamTimer); S.streamTimer = null; }
    if (S.snapshotURL) {
      try { URL.revokeObjectURL(S.snapshotURL); } catch (e) { /* ignore */ }
      S.snapshotURL = "";
    }
    S.snapshotBusy = false;
    if (S.pc) {
      try { S.pc.close(); } catch (e) { /* ignore */ }
      S.pc = null;
    }
    var viewer = S.viewerID;
    S.viewerID = "";
    if (viewer && S.token) {
      try {
        fetch("/v1/stream/webrtc/" + encodeURIComponent(viewer), {
          method: "DELETE",
          headers: { "Authorization": "Bearer " + S.token, "X-Chopin-Client": "webui" },
          keepalive: true
        });
      } catch (e) { /* ignore */ }
    }
    S.mode = "none";
  }

  function startSnapshot(reason) {
    stopStream();
    S.mode = "snapshot";
    S.snapshot = true;
    video.hidden = true;
    snapshot.hidden = false;
    if (reason) S.bannerError = reason;
    render();
    var tick = async function () {
      if (!S.snapshot || document.hidden || S.snapshotBusy || !S.token) return;
      S.snapshotBusy = true;
      try {
        var res = await fetch("/v1/ui/frame?display_id=" + S.displayID + "&format=jpeg&quality=75", {
          headers: { "Authorization": "Bearer " + S.token, "X-Chopin-Client": "webui" },
          cache: "no-store"
        });
        if (res.status === 401) {
          showLogin(window.t("tokenInvalid"));
          return;
        }
        if (!res.ok) throw new Error("HTTP " + res.status);
        var blob = await res.blob();
        var url = URL.createObjectURL(blob);
        var old = S.snapshotURL;
        S.snapshotURL = url;
        snapshot.src = url;
        if (old) {
          try { URL.revokeObjectURL(old); } catch (e) { /* ignore */ }
        }
      } catch (e) {
        /* keep the last frame; the next tick retries */
      } finally {
        S.snapshotBusy = false;
      }
    };
    tick();
    S.snapshotTimer = setInterval(tick, 500);
  }

  function canInput() {
    return Boolean(S.token && S.sessionActive && !S.holder && !S.claiming);
  }

  function activeMedia() {
    return video.hidden ? snapshot : video;
  }

  function mediaSize(el) {
    var w = el === video ? (video.videoWidth || S.width) : (snapshot.naturalWidth || S.width);
    var h = el === video ? (video.videoHeight || S.height) : (snapshot.naturalHeight || S.height);
    return { w: w || 0, h: h || 0 };
  }

  function toDevice(clientX, clientY) {
    var el = activeMedia();
    var size = mediaSize(el);
    if (!size.w || !size.h) return null;
    var rect = el.getBoundingClientRect();
    var scale = Math.min(rect.width / size.w, rect.height / size.h);
    if (!scale || !isFinite(scale)) return null;
    var contentW = size.w * scale;
    var contentH = size.h * scale;
    var left = rect.left + (rect.width - contentW) / 2;
    var top = rect.top + (rect.height - contentH) / 2;
    var x = Math.round((clientX - left) / scale);
    var y = Math.round((clientY - top) / scale);
    x = Math.max(0, Math.min(size.w - 1, x));
    y = Math.max(0, Math.min(size.h - 1, y));
    return { x: x, y: y };
  }

  async function sendTap(p) {
    if (!canInput() || !p) return;
    try {
      await api("/v1/input/tap", { method: "POST", body: { x: p.x, y: p.y, display_id: S.displayID } });
    } catch (e) { handleInputError(e); }
  }

  async function sendSwipe(a, b, ms) {
    if (!canInput() || !a || !b) return;
    try {
      await api("/v1/input/swipe", {
        method: "POST",
        body: { x1: a.x, y1: a.y, x2: b.x, y2: b.y, ms: ms, display_id: S.displayID }
      });
    } catch (e) { handleInputError(e); }
  }

  async function sendLong(p) {
    if (!canInput() || !p) return;
    try {
      await api("/v1/input/longpress", {
        method: "POST",
        body: { x: p.x, y: p.y, ms: 600, display_id: S.displayID }
      });
    } catch (e) { handleInputError(e); }
  }

  async function sendKey(key) {
    if (!canInput()) return;
    try {
      await api("/v1/input/key", { method: "POST", body: { key: key, display_id: S.displayID } });
    } catch (e) { handleInputError(e); }
  }

  async function sendText() {
    if (!canInput()) return;
    var text = textInput.value;
    if (!text) return;
    try {
      var result = await api("/v1/input/text", {
        method: "POST",
        body: { text: text, display_id: S.displayID, clear: false, submit: false }
      });
      textInput.value = "";
      inputHint.textContent = result && result.backend ? ("backend: " + result.backend) : "";
    } catch (e) { handleInputError(e); }
  }

  async function clearText() {
    if (!canInput()) return;
    try {
      await api("/v1/input/text", {
        method: "POST",
        body: { text: "", display_id: S.displayID, clear: true, submit: false }
      });
      textInput.value = "";
    } catch (e) { handleInputError(e); }
  }

  function handleInputError(e) {
    if (e.code === "session_required" || e.code === "unauthorized") {
      S.session = null;
      S.sessionActive = false;
      storageRemove(SESSION_KEY);
      S.bannerError = window.t("heartbeatLost");
      render();
      return;
    }
    S.bannerError = errorText(e);
    render();
  }

  // ---- device readings -----------------------------------------------------
  //
  // The rail answers "is this thing healthy" without making the person parse a
  // status JSON. Each cell is a value plus a state class, so the odd one out is
  // visible at a glance instead of needing to be read.
  var lastStatus = null;

  function formatUptime(sec) {
    if (!sec || sec < 0) return "—";
    var d = Math.floor(sec / 86400);
    var h = Math.floor((sec % 86400) / 3600);
    var m = Math.floor((sec % 3600) / 60);
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + m + "m";
    return m + "m";
  }

  function setCell(el, text, state) {
    if (!el) return;
    el.textContent = text;
    el.className = "v " + (state || "idle");
  }

  function renderRail(st) {
    lastStatus = st;
    if (!st) {
      setCell(dDaemon, "—", "idle");
      setCell(dA11y, "—", "idle");
      setCell(dBg, "—", "idle");
      setCell(dSessions, "—", "idle");
      setCell(dUptime, "—", "idle");
      return;
    }
    var d = st.daemon || {};
    var a = st.app || {};
    var a11y = a.a11y || {};
    var bg = a.bg || {};
    var sessions = st.sessions || {};

    // "运行中" alone hides the interesting part: the pid changes when the daemon
    // restarts unasked, and that is exactly the thing worth noticing.
    setCell(dDaemon, (d.state || "?") + (d.pid ? " " + d.pid : ""),
      d.state === "running" ? "ok" : (d.state ? "bad" : "idle"));
    setCell(dA11y, a11y.state || "—", a11y.state === "OK" ? "ok" : (a11y.state ? "warn" : "idle"));
    setCell(dBg, bg.ok ? "OK" : (a.bg ? "受限" : "—"), bg.ok ? "ok" : (a.bg ? "warn" : "idle"));
    setCell(dSessions, (sessions.active || 0) + "/" + (sessions.max || 0),
      (sessions.active || 0) > 0 ? "ok" : "idle");
    setCell(dUptime, formatUptime(d.uptime_sec), "idle");
  }

  // ---- the agent's current action -----------------------------------------
  //
  // This is the same sentence the phone's own overlay shows. It is here because
  // the person watching this page is the one who has to decide whether to press
  // 释放, and "someone is connected" does not answer that.
  function renderActivity(hud) {
    if (!activity) return;
    var state = "idle";
    var what = window.t("activityIdle");
    if (hud && hud.stopped) {
      state = "stopped";
      what = window.t("activityStopped");
    } else if (hud && hud.paused) {
      state = "paused";
      what = window.t("activityPaused");
    } else if (hud && hud.action) {
      state = "live";
      what = hud.action;
    } else if (hud && hud.visible) {
      state = "live";
      what = window.t("activityLive");
    }
    activity.className = "activity" + (state === "idle" ? "" : " " + state);
    activityWhat.textContent = what;
    if (hud && hud.agent) {
      activityMeta.textContent = hud.agent + (hud.display ? " · " + window.t("display") + " " + hud.display : "");
    } else {
      activityMeta.textContent = "";
    }
  }

  async function pollStatus() {
    try {
      var res = await fetch("/v1/status", {
        cache: "no-store",
        headers: S.token ? { "Authorization": "Bearer " + S.token } : {}
      });
      if (!res.ok) { renderRail(null); return; }
      renderRail(await res.json());
    } catch (e) {
      renderRail(null);
    }
  }

  async function pollActivity() {
    // The HUD state is the daemon's own view of the overlay, and it carries the
    // current action. A page that cannot read it still works; the line just says
    // nothing rather than lying.
    try {
      var res = await fetch("/v1/hud", {
        cache: "no-store",
        headers: S.token ? { "Authorization": "Bearer " + S.token } : {}
      });
      if (!res.ok) { renderActivity(null); return; }
      var body = await res.json();
      renderActivity(body && body.state ? body.state : body);
    } catch (e) {
      renderActivity(null);
    }
  }

  // The URL bar shows the address another machine must use. It is derived, not
  // stored: the daemon serves on the host it was reached through, so the browser
  // already knows the right host — only the path and port need saying.
  function renderAccess() {
    if (!accessUrl) return;
    accessUrl.textContent = window.location.origin + "/ui/";
  }

  function wireCopy() {
    if (!copyUrlBtn) return;
    copyUrlBtn.addEventListener("click", function () {
      var text = window.location.origin + "/ui/";
      var done = function () {
        copyUrlBtn.textContent = window.t("copied");
        setTimeout(function () { copyUrlBtn.textContent = window.t("copyUrl"); }, 1600);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () { fallbackCopy(text, done); });
      } else {
        fallbackCopy(text, done);
      }
    });
  }

  // Older WebViews and plain-HTTP contexts have no clipboard API; a hidden
  // textarea plus execCommand is the only route there, and a copy button that does
  // nothing is worse than no button.
  function fallbackCopy(text, done) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); done(); } catch (e) { /* nothing else to try */ }
    document.body.removeChild(ta);
  }

  function setBadge(state) {
    statusBadge.className = "badge";
    if (state === "active") {
      statusBadge.classList.add("active");
      statusBadge.textContent = window.t("active");
    } else if (state === "readonly") {
      statusBadge.classList.add("readonly");
      statusBadge.textContent = window.t("readOnly");
    } else if (state === "free") {
      statusBadge.classList.add("free");
      statusBadge.textContent = window.t("free");
    } else {
      statusBadge.textContent = window.t("connecting");
    }
  }

  function setControls(enabled) {
    textInput.disabled = !enabled;
    sendTextBtn.disabled = !enabled;
    clearTextBtn.disabled = !enabled;
    keyButtons.forEach(function (b) { b.disabled = !enabled; });
  }

  function displayLabel(id) {
    return id === 0 ? window.t("main") : (window.t("virtual") + " " + id);
  }

  function render() {
    var active = S.sessionActive;
    var holder = S.holder;
    if (active) setBadge("active");
    else if (holder) setBadge("readonly");
    else setBadge("free");

    claimBtn.hidden = active;
    releaseBtn.hidden = !active;
    claimBtn.disabled = !S.token || S.claiming || Boolean(holder);
    displaySelect.disabled = active || S.claiming;

    if (active) {
      sessionInfo.textContent = window.t("you") + " · " + displayLabel(S.displayID);
    } else if (holder) {
      sessionInfo.textContent = window.t("holder") + ": " + (holder.agent || "?") + "/" + (holder.tag || "?") + " · " + displayLabel(holder.display);
    } else {
      sessionInfo.textContent = window.t("noSession");
    }

    var bannerMessage = "";
    if (S.snapshot) bannerMessage = window.t("snapshotBanner");
    else if (S.bannerError) bannerMessage = S.bannerError;
    else if (holder && !active) bannerMessage = window.t("sessionConflict");
    banner.hidden = !bannerMessage;
    bannerText.textContent = bannerMessage;
    retryStreamBtn.hidden = !S.snapshot;

    stage.classList.toggle("active", canInput());
    stageHint.hidden = canInput();
    inputHint.textContent = canInput() ? "" : (holder ? window.t("sessionConflict") : window.t("noSession"));
    setControls(canInput());
  }

  function onPointerDown(event) {
    if (!canInput() || !event.isPrimary) return;
    if (S.pointerId !== null) return;
    S.pointerId = event.pointerId;
    S.gesture = {
      startX: event.clientX,
      startY: event.clientY,
      startedAt: performance.now(),
      moved: false,
      longFired: false
    };
    try { stage.setPointerCapture(event.pointerId); } catch (e) { /* ignore */ }
    if (S.longTimer) clearTimeout(S.longTimer);
    var pointerId = event.pointerId;
    var startX = event.clientX;
    var startY = event.clientY;
    S.longTimer = setTimeout(function () {
      if (!S.gesture || S.pointerId !== pointerId || S.gesture.moved) return;
      S.gesture.longFired = true;
      sendLong(toDevice(startX, startY));
    }, 600);
    event.preventDefault();
  }

  function onPointerMove(event) {
    if (S.pointerId !== event.pointerId || !S.gesture) return;
    var dx = event.clientX - S.gesture.startX;
    var dy = event.clientY - S.gesture.startY;
    if (dx * dx + dy * dy > 64) {
      S.gesture.moved = true;
      if (S.longTimer) { clearTimeout(S.longTimer); S.longTimer = null; }
    }
    event.preventDefault();
  }

  function onPointerUp(event) {
    if (S.pointerId !== event.pointerId || !S.gesture) return;
    if (S.longTimer) { clearTimeout(S.longTimer); S.longTimer = null; }
    var gesture = S.gesture;
    var start = toDevice(gesture.startX, gesture.startY);
    var end = toDevice(event.clientX, event.clientY);
    S.pointerId = null;
    S.gesture = null;
    try { stage.releasePointerCapture(event.pointerId); } catch (e) { /* ignore */ }
    if (!start || !end) return;
    if (gesture.moved) {
      var ms = Math.max(50, Math.min(2000, Math.round(performance.now() - gesture.startedAt)));
      sendSwipe(start, end, ms);
    } else if (!gesture.longFired) {
      sendTap(start);
    }
    event.preventDefault();
  }

  function onPointerCancel(event) {
    if (S.pointerId !== event.pointerId) return;
    if (S.longTimer) { clearTimeout(S.longTimer); S.longTimer = null; }
    S.pointerId = null;
    S.gesture = null;
  }

  function wire() {
    loginBtn.addEventListener("click", login);
    tokenInput.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) login();
    });
    logoutBtn.addEventListener("click", logout);
    claimBtn.addEventListener("click", claim);
    releaseBtn.addEventListener("click", function () { releaseSession(false); });
    retryStreamBtn.addEventListener("click", function () { connectStream(); });
    displaySelect.addEventListener("change", async function () {
      var next = Number(displaySelect.value);
      if (next === S.displayID) return;
      if (S.session) await releaseSession(true);
      S.displayID = next;
      await connectStream();
      try { await loadSessions(); } catch (e) { /* keep UI */ }
    });
    sendTextBtn.addEventListener("click", sendText);
    clearTextBtn.addEventListener("click", clearText);
    keyButtons.forEach(function (button) {
      button.addEventListener("click", function () { sendKey(button.getAttribute("data-key")); });
    });
    stage.addEventListener("pointerdown", onPointerDown);
    stage.addEventListener("pointermove", onPointerMove);
    stage.addEventListener("pointerup", onPointerUp);
    stage.addEventListener("pointercancel", onPointerCancel);
    stage.addEventListener("contextmenu", function (event) { event.preventDefault(); });
    window.addEventListener("pagehide", releaseBeacon);
  }

  async function boot() {
    window.applyI18n();
    wire();
    var token = storageGet(TOKEN_KEY);
    var saved = storageGet(SESSION_KEY);
    if (saved) {
      try {
        var parsed = JSON.parse(saved);
        if (parsed && parsed.id && parsed.token) S.session = parsed;
      } catch (e) { storageRemove(SESSION_KEY); }
    }
    if (!token) {
      showLogin("");
      return;
    }
    S.token = token;
    try {
      await start();
    } catch (e) {
      S.token = "";
      storageRemove(TOKEN_KEY);
      showLogin(errorText(e));
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
