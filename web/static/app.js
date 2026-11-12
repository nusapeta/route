(function () {
  "use strict";

  var JAKARTA = { lat: -6.2, lng: 106.816666 };
  var PROFILE_LABELS = { mobil: "mobil", motor: "motor" };

  var els = {
    panel: document.querySelector(".panel"),
    hint: document.getElementById("hint"),
    status: document.getElementById("status"),
    fromCoord: document.getElementById("from-coord"),
    toCoord: document.getElementById("to-coord"),
    routeButton: document.getElementById("route-button"),
    resetButton: document.getElementById("reset-button"),
    shareButton: document.getElementById("share-button"),
    result: document.getElementById("result"),
    region: document.getElementById("result-region"),
    distance: document.getElementById("result-distance"),
    duration: document.getElementById("result-duration"),
    speed: document.getElementById("result-speed"),
    serverMs: document.getElementById("result-server-ms"),
    latencyMs: document.getElementById("result-latency-ms"),
    expansions: document.getElementById("result-expansions"),
    snapFrom: document.getElementById("result-snap-from"),
    snapTo: document.getElementById("result-snap-to"),
    badges: document.getElementById("result-badges"),
    note: document.getElementById("result-note"),
    profileButtons: Array.prototype.slice.call(document.querySelectorAll("[data-profile]"))
  };

  var state = {
    from: null,
    to: null,
    profile: "mobil",
    loading: false,
    error: false,
    result: null,
    request: null,
    sequence: 0
  };

  var fromMarker = null;
  var toMarker = null;
  var routeLayer = null;

  if (typeof L === "undefined") {
    setStatus("Pustaka peta gagal dimuat. Muat ulang halaman.", "error");
    return;
  }

  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  var map = L.map("map", {
    zoomControl: false,
    attributionControl: false,
    minZoom: 4,
    maxZoom: 19,
    zoomAnimation: !reduceMotion,
    fadeAnimation: !reduceMotion,
    markerZoomAnimation: !reduceMotion
  }).setView([JAKARTA.lat, JAKARTA.lng], 11);

  L.control.zoom({
    position: "topleft",
    zoomInTitle: "Perbesar peta",
    zoomOutTitle: "Perkecil peta"
  }).addTo(map);
  L.control.attribution({ position: "topright" }).addTo(map);
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
  }).addTo(map);

  map.on("click", function (event) {
    addMapPoint(event.latlng);
  });

  var mapElement = map.getContainer();
  if (!mapElement.hasAttribute("tabindex")) {
    mapElement.setAttribute("tabindex", "0");
  }
  mapElement.addEventListener("keydown", function (event) {
    if (event.key !== "Enter" || event.target !== mapElement) {
      return;
    }
    event.preventDefault();
    addMapPoint(map.getCenter());
  });

  els.routeButton.addEventListener("click", calculateRoute);
  els.resetButton.addEventListener("click", resetAll);
  els.shareButton.addEventListener("click", copyLink);

  els.profileButtons.forEach(function (button) {
    button.addEventListener("click", function () {
      setProfile(button.dataset.profile);
    });
  });

  restoreFromURL();

  function restoreFromURL() {
    var params = new URLSearchParams(window.location.search);
    var from = parsePoint(params.get("from"));
    var to = parsePoint(params.get("to"));
    state.profile = params.get("profile") === "motor" ? "motor" : "mobil";
    state.from = from;
    state.to = to;

    syncMarker("from");
    syncMarker("to");
    refresh();
    updateURL();

    if (state.from || state.to) {
      fitPoints();
    }
    if (state.from && state.to) {
      calculateRoute();
    } else {
      setStatus(nextStepText(), "neutral");
    }
  }

  function addMapPoint(latlng) {
    var point = { lat: latlng.lat, lng: latlng.lng };
    if (!state.from) {
      state.from = point;
    } else if (!state.to) {
      state.to = point;
    } else {
      state.from = point;
      state.to = null;
    }

    syncMarker("from");
    syncMarker("to");
    pointChanged();
  }

  function pointChanged() {
    cancelRequest();
    clearRoute();
    state.result = null;
    state.error = false;
    hideResult();
    updateURL();
    refresh();
    setStatus(nextStepText(), "neutral");
    revealInPanel(els.hint);
  }

  function syncMarker(kind) {
    var point = state[kind];
    var marker = kind === "from" ? fromMarker : toMarker;

    if (!point) {
      if (marker) {
        map.removeLayer(marker);
        if (kind === "from") {
          fromMarker = null;
        } else {
          toMarker = null;
        }
      }
      return;
    }

    if (!marker) {
      var label = kind === "from" ? "Titik awal" : "Titik tujuan";
      marker = L.marker([point.lat, point.lng], {
        draggable: true,
        icon: markerIcon(kind),
        keyboard: false,
        title: label,
        riseOnHover: true
      }).addTo(map);

      var element = marker.getElement();
      if (element) {
        element.setAttribute("aria-label", label);
      }

      marker.on("dragend", function () {
        var moved = marker.getLatLng();
        state[kind] = { lat: moved.lat, lng: moved.lng };
        pointChanged();
      });

      if (kind === "from") {
        fromMarker = marker;
      } else {
        toMarker = marker;
      }
      return;
    }

    marker.setLatLng([point.lat, point.lng]);
  }

  function markerIcon(kind) {
    return L.divIcon({
      className: "route-pin",
      html: '<span class="pin pin--' + kind + '">' + (kind === "from" ? "A" : "B") + "</span>",
      iconSize: [44, 44],
      iconAnchor: [22, 22]
    });
  }

  function setProfile(name) {
    if (name !== "mobil" && name !== "motor") {
      return;
    }
    if (state.profile === name) {
      return;
    }
    state.profile = name;
    state.error = false;
    updateURL();
    refresh();

    if (state.from && state.to) {
      calculateRoute();
    } else {
      setStatus(nextStepText(), "neutral");
    }
  }

  function calculateRoute() {
    if (!state.from || !state.to) {
      return;
    }

    cancelRequest();
    var sequence = state.sequence;
    var controller = new AbortController();
    state.request = controller;
    state.loading = true;
    state.error = false;
    state.result = null;
    clearRoute();
    hideResult();
    refresh();
    setStatus("Menghitung rute " + PROFILE_LABELS[state.profile] + ".", "loading");

    var query = new URLSearchParams({
      from: formatPoint(state.from),
      to: formatPoint(state.to),
      format: "geojson",
      profile: state.profile
    });
    var startedAt = performance.now();

    fetch("/route?" + query.toString(), {
      signal: controller.signal,
      headers: { Accept: "application/json" }
    }).then(function (response) {
      return response.json().catch(function () {
        return null;
      }).then(function (body) {
        return { response: response, body: body };
      });
    }).then(function (reply) {
      if (sequence !== state.sequence) {
        return;
      }
      var response = reply.response;
      var body = reply.body;

      if (!response.ok) {
        var serverMessage = body && typeof body.error === "string" ? body.error : "";
        var reason = body && typeof body.reason === "string" ? body.reason : "";
        throw new Error(routeErrorMessage(response.status, reason, serverMessage));
      }
      if (!body || typeof body !== "object") {
        throw new Error("Respons rute tidak valid.");
      }
      if (!body.geojson || body.geojson.type !== "FeatureCollection") {
        throw new Error("Respons rute tidak memuat geometri.");
      }

      state.loading = false;
      state.request = null;
      state.result = body;
      drawRoute(body);
      var requestURL = "/route?" + query.toString();
      showResult(body, {
        latencyMs: performance.now() - startedAt,
        serverMs: headerNumber(response, "X-Route-Ms"),
        expansions: headerNumber(response, "X-Route-Expansions"),
        cached: isCachedResponse(requestURL)
      });
      refresh();
      setStatus("Rute ditemukan.", "success");
      revealInPanel(els.result);
    }).catch(function (error) {
      if (error && error.name === "AbortError") {
        return;
      }
      if (sequence !== state.sequence) {
        return;
      }

      state.loading = false;
      state.request = null;
      state.error = true;
      state.result = null;
      clearRoute();
      hideResult();
      refresh();

      var message = error instanceof TypeError
        ? "Tidak bisa menghubungi server rute. Periksa koneksi lalu coba lagi."
        : (error && error.message ? error.message : "Terjadi kesalahan yang tidak diketahui.");
      setStatus("Gagal menghitung rute. " + message, "error");
      revealInPanel(els.status);
    });
  }

  function cancelRequest() {
    state.sequence += 1;
    if (state.request) {
      state.request.abort();
      state.request = null;
    }
    state.loading = false;
  }

  function drawRoute(body) {
    clearRoute();

    var group = L.layerGroup();
    var casing = L.geoJSON(body.geojson, {
      style: {
        color: "#fffdf8",
        weight: 8,
        opacity: 0.9,
        lineCap: "round",
        lineJoin: "round"
      }
    });
    var line = L.geoJSON(body.geojson, {
      style: {
        color: "#0b5d45",
        weight: 4,
        opacity: 1,
        lineCap: "round",
        lineJoin: "round"
      }
    });

    group.addLayer(casing);
    group.addLayer(line);
    addSnapLine(group, state.from, body.snap_from);
    addSnapLine(group, state.to, body.snap_to);
    group.addTo(map);
    routeLayer = group;
    fitRoute(body);
  }

  function addSnapLine(group, point, snap) {
    if (!point || !Array.isArray(snap) || snap.length < 2) {
      return;
    }
    var lat = Number(snap[0]);
    var lng = Number(snap[1]);
    if (!Number.isFinite(lat) || !Number.isFinite(lng)) {
      return;
    }
    if (map.distance([point.lat, point.lng], [lat, lng]) < 20) {
      return;
    }
    group.addLayer(L.polyline([
      [point.lat, point.lng],
      [lat, lng]
    ], {
      color: "#556057",
      weight: 2,
      opacity: 0.85,
      dashArray: "3 6"
    }));
  }

  function clearRoute() {
    if (routeLayer) {
      map.removeLayer(routeLayer);
      routeLayer = null;
    }
  }

  function fitPoints() {
    var bounds = L.latLngBounds([]);
    if (state.from) {
      bounds.extend([state.from.lat, state.from.lng]);
    }
    if (state.to) {
      bounds.extend([state.to.lat, state.to.lng]);
    }
    if (bounds.isValid()) {
      fitBounds(bounds);
    }
  }

  function fitRoute(body) {
    var bounds = null;
    var box = body.bbox;

    if (Array.isArray(box) && box.length === 4 && box.every(Number.isFinite)) {
      bounds = L.latLngBounds([box[0], box[1]], [box[2], box[3]]);
    } else if (routeLayer) {
      bounds = routeLayer.getBounds();
    }

    if (!bounds || !bounds.isValid()) {
      return;
    }
    if (state.from) {
      bounds.extend([state.from.lat, state.from.lng]);
    }
    if (state.to) {
      bounds.extend([state.to.lat, state.to.lng]);
    }
    fitBounds(bounds);
  }

  function fitBounds(bounds) {
    var sidePanel = window.matchMedia("(min-width: 720px)").matches;
    var panelHeight = sidePanel ? 0 : els.panel.getBoundingClientRect().height;
    map.fitBounds(bounds, {
      paddingTopLeft: [32, 48],
      paddingBottomRight: [32, panelHeight + 28],
      maxZoom: 16,
      animate: !reduceMotion
    });
  }

  function revealInPanel(element) {
    if (!element || element.hidden) {
      return;
    }
    if (window.matchMedia("(min-width: 720px)").matches) {
      return;
    }
    var panel = els.panel.getBoundingClientRect();
    var box = element.getBoundingClientRect();
    var style = window.getComputedStyle(els.panel);
    var top = panel.top + parseFloat(style.paddingTop);
    var bottom = panel.bottom - parseFloat(style.paddingBottom);
    if (box.top < top) {
      els.panel.scrollTop += box.top - top;
    } else if (box.bottom > bottom) {
      els.panel.scrollTop += box.bottom - bottom;
    }
  }

  function showResult(body, timing) {
    els.result.hidden = false;

    var region = typeof body.region === "string" ? body.region : "";
    els.region.textContent = region;
    els.region.hidden = region === "";

    setText(els.distance, formatDistance(body.distance_m));
    setText(els.duration, formatDuration(body.duration_s));
    setText(els.speed, formatSpeed(body.distance_m, body.duration_s));
    setText(els.serverMs, markCached(formatMilliseconds(timing.serverMs), timing.cached));
    setText(els.latencyMs, markCached(formatMilliseconds(timing.latencyMs), timing.cached));
    setText(els.expansions, formatCount(timing.expansions));
    setText(els.snapFrom, formatMeters(body.snap_from_m));
    setText(els.snapTo, formatMeters(body.snap_to_m));

    els.badges.replaceChildren();
    if (body.fallback) {
      addBadge("Fallback belok", "Larangan belok dilepas agar rute ditemukan.", true);
    }
    if (body.relaxed) {
      addBadge("Pencarian longgar", "Heuristik pencarian dilonggarkan agar rute ditemukan.", true);
    }
    if (body.has_ferry || (Number.isFinite(body.ferry_m) && body.ferry_m > 0)) {
      var ferry = Number.isFinite(body.ferry_m) && body.ferry_m > 0
        ? "Feri " + formatDistance(body.ferry_m)
        : "Feri";
      addBadge(ferry, "Rute memakai penyeberangan feri.", false);
    }
    els.badges.hidden = els.badges.childElementCount === 0;

    var notes = [];
    if (body.fallback) {
      notes.push("Rute memakai fallback: larangan belok dilepas agar rute ditemukan.");
    }
    if (body.relaxed) {
      var relaxed = "Rute memakai pencarian longgar";
      if (Number.isFinite(body.epsilon)) {
        relaxed += " dengan epsilon " + formatNumber(body.epsilon);
      }
      if (Number.isFinite(body.turn_penalty)) {
        relaxed += " dan penalti belok " + formatNumber(body.turn_penalty);
      }
      notes.push(relaxed + "; hasil bisa bukan yang terpendek.");
    }
    els.note.textContent = notes.join(" ");
    els.note.hidden = notes.length === 0;
  }

  function addBadge(label, title, warn) {
    var badge = document.createElement("span");
    badge.className = warn ? "badge badge--warn" : "badge";
    badge.textContent = label;
    if (title) {
      badge.title = title;
    }
    els.badges.appendChild(badge);
  }

  function setText(element, value) {
    element.textContent = value === null ? "Tidak tersedia" : value;
  }

  function hideResult() {
    els.result.hidden = true;
    els.badges.replaceChildren();
    els.badges.hidden = true;
    els.note.textContent = "";
    els.note.hidden = true;
  }

  function resetAll() {
    cancelRequest();
    state.from = null;
    state.to = null;
    state.error = false;
    state.result = null;
    syncMarker("from");
    syncMarker("to");
    clearRoute();
    hideResult();
    updateURL();
    refresh();
    setStatus("Pilih titik awal dan tujuan di peta.", "neutral");
    revealInPanel(els.hint);
  }

  function copyLink() {
    if (!state.from || !state.to) {
      return;
    }
    var link = window.location.href;
    var clipboard = navigator.clipboard;

    if (!clipboard || typeof clipboard.writeText !== "function") {
      window.prompt("Salin tautan rute ini:", link);
      setStatus("Tautan ditampilkan untuk disalin.", "neutral");
      return;
    }

    clipboard.writeText(link).then(function () {
      setStatus("Tautan rute disalin.", "success");
    }).catch(function () {
      window.prompt("Salin tautan rute ini:", link);
      setStatus("Tautan ditampilkan untuk disalin.", "neutral");
    });
  }

  function refresh() {
    updatePointLabels();
    updateProfileButtons();
    updateControls();
    els.hint.textContent = nextStepText();
  }

  function updatePointLabels() {
    els.fromCoord.textContent = state.from ? formatPointLabel(state.from) : "Belum dipilih";
    els.toCoord.textContent = state.to ? formatPointLabel(state.to) : "Belum dipilih";
  }

  function updateProfileButtons() {
    els.profileButtons.forEach(function (button) {
      button.setAttribute("aria-pressed", button.dataset.profile === state.profile ? "true" : "false");
    });
  }

  function updateControls() {
    var ready = Boolean(state.from && state.to);
    els.routeButton.disabled = !ready || state.loading;
    els.routeButton.setAttribute("aria-busy", state.loading ? "true" : "false");
    els.shareButton.disabled = !ready;
    els.resetButton.disabled = !state.loading && !state.from && !state.to && !state.result;
  }

  function nextStepText() {
    if (state.loading) {
      return "Menghitung rute " + PROFILE_LABELS[state.profile] + ".";
    }
    if (state.error) {
      return "Periksa titik atau coba lagi.";
    }
    if (!state.from) {
      return "Klik peta untuk memilih titik awal.";
    }
    if (!state.to) {
      return "Klik peta untuk memilih titik tujuan.";
    }
    if (state.result) {
      return "Klik peta untuk memulai rute baru.";
    }
    return "Tekan Hitung rute untuk melihat hasil.";
  }

  function setStatus(message, tone) {
    els.status.textContent = message;
    els.status.dataset.tone = tone;
  }

  function updateURL() {
    var query = new URLSearchParams();
    if (state.from) {
      query.set("from", formatPoint(state.from));
    }
    if (state.to) {
      query.set("to", formatPoint(state.to));
    }
    if (state.profile === "motor") {
      query.set("profile", "motor");
    }
    var search = query.toString();
    var next = window.location.pathname + (search ? "?" + search : "");
    window.history.replaceState(null, "", next);
  }

  function parsePoint(value) {
    if (!value) {
      return null;
    }
    var parts = value.split(",");
    if (parts.length !== 2) {
      return null;
    }
    var lat = Number(parts[0]);
    var lng = Number(parts[1]);
    if (!Number.isFinite(lat) || !Number.isFinite(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180) {
      return null;
    }
    return { lat: lat, lng: lng };
  }

  function formatPoint(point) {
    return point.lat.toFixed(6) + "," + point.lng.toFixed(6);
  }

  function formatPointLabel(point) {
    return point.lat.toFixed(5) + ", " + point.lng.toFixed(5);
  }

  function formatDistance(value) {
    var meters = Number(value);
    if (!Number.isFinite(meters) || meters < 0) {
      return null;
    }
    if (meters < 1000) {
      return Math.round(meters).toLocaleString("id-ID") + " m";
    }
    return (meters / 1000).toLocaleString("id-ID", {
      minimumFractionDigits: 0,
      maximumFractionDigits: 2
    }) + " km";
  }

  function formatMeters(value) {
    var meters = Number(value);
    if (!Number.isFinite(meters) || meters < 0) {
      return null;
    }
    return Math.round(meters).toLocaleString("id-ID") + " m";
  }

  function formatDuration(value) {
    var seconds = Number(value);
    if (!Number.isFinite(seconds) || seconds < 0) {
      return null;
    }
    if (seconds === 0) {
      return "0 mnt";
    }
    if (seconds < 60) {
      return "<1 mnt";
    }
    var total = Math.round(seconds / 60);
    if (total < 60) {
      return total + " mnt";
    }
    var hours = Math.floor(total / 60);
    var minutes = total % 60;
    return minutes === 0 ? hours + " j" : hours + " j " + minutes + " mnt";
  }

  function markCached(value, cached) {
    return value === null || !cached ? value : value + " (cache)";
  }

  function isCachedResponse(url) {
    var entries = performance.getEntriesByName(new URL(url, window.location.href).href);
    var entry = entries[entries.length - 1];
    if (!entry) {
      return false;
    }
    return entry.deliveryType === "cache" || (entry.transferSize === 0 && entry.decodedBodySize > 0);
  }

  function formatSpeed(meters, seconds) {
    var distance = Number(meters);
    var duration = Number(seconds);
    if (!Number.isFinite(distance) || distance < 0 || !Number.isFinite(duration) || duration <= 0) {
      return null;
    }
    var kmh = (distance / 1000) / (duration / 3600);
    return kmh.toLocaleString("id-ID", {
      minimumFractionDigits: 0,
      maximumFractionDigits: 1
    }) + " km/j";
  }

  function formatMilliseconds(value) {
    if (!Number.isFinite(value) || value < 0) {
      return null;
    }
    if (value < 10) {
      return value.toLocaleString("id-ID", {
        minimumFractionDigits: 0,
        maximumFractionDigits: 1
      }) + " ms";
    }
    return Math.round(value).toLocaleString("id-ID") + " ms";
  }

  function formatCount(value) {
    if (!Number.isFinite(value) || value < 0) {
      return null;
    }
    return Math.round(value).toLocaleString("id-ID");
  }

  function headerNumber(response, name) {
    var raw = response.headers.get(name);
    if (raw === null || raw.trim() === "") {
      return null;
    }
    var value = Number(raw);
    return Number.isFinite(value) && value >= 0 ? value : null;
  }

  function formatNumber(value) {
    return Number(value).toLocaleString("id-ID", {
      maximumFractionDigits: 2
    });
  }

  var ROUTE_REASON_MESSAGES = {
    snap: "Titik terlalu jauh dari jalan. Geser pin ke jalan terdekat.",
    too_far: "Rute terlalu panjang. Pilih titik yang lebih dekat.",
    budget: "Rute terlalu panjang untuk dihitung. Pilih titik yang lebih dekat.",
    no_route: "Tidak ada rute jalan antara kedua titik. Geser pin ke jalan terdekat.",
    cross_region: "Kedua titik berada di wilayah yang belum terhubung. Pilih titik di pulau yang sama.",
    deadline: "Perhitungan rute melewati batas waktu. Coba titik yang lebih dekat."
  };

  function routeErrorMessage(status, reason, serverMessage) {
    if (reason && typeof ROUTE_REASON_MESSAGES[reason] === "string") {
      return ROUTE_REASON_MESSAGES[reason];
    }
    var message;
    switch (status) {
      case 400:
        message = "Permintaan rute tidak valid.";
        break;
      case 422:
        message = "Titik tidak bisa dirutekan.";
        break;
      case 429:
        message = "Terlalu banyak permintaan. Tunggu sebentar lalu coba lagi.";
        break;
      case 502:
        message = "Layanan rute tidak tersedia.";
        break;
      case 504:
        message = "Perhitungan rute melewati batas waktu. Coba titik yang lebih dekat.";
        break;
      default:
        message = "Permintaan rute gagal (HTTP " + status + ").";
    }
    return serverMessage ? message + " " + serverMessage : message;
  }
})();
