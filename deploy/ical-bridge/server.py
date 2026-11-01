#!/usr/bin/env python3
"""HEARTH Configurable OAuth-to-iCal, Mealie Recipe + Image Proxy & Plex/Arr/Seerr Bridge."""

import socket

from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor
import datetime
import ipaddress
import json
import os
import re
import sys
import threading
import time
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from zoneinfo import ZoneInfo
try:
    import yaml
except ImportError:
    yaml = None

try:
    PT = ZoneInfo(os.environ.get("TZ", "UTC"))
except Exception:
    PT = ZoneInfo("UTC")
UTC = datetime.timezone.utc

CACHE_LOCK = threading.Lock()
CACHE = {}
IMG_CACHE = OrderedDict()
IMG_CACHE_LOCK = threading.Lock()
MAX_IMG_CACHE_ENTRIES = 100
CACHE_TTL = 300
IMG_CACHE_TTL = 3600
FETCH_LOCKS = {}
FETCH_LOCKS_GUARD = threading.Lock()
TOKEN_LOCK = threading.Lock()
TOKEN_CACHE = {"token": None, "exp": 0}

PLEX_THUMB_RE = re.compile(r"^/library/(metadata|arts|parts)/[0-9a-zA-Z_/.-]+$")
RECIPE_ID_RE = re.compile(r"^[0-9a-zA-Z_-]+$")
YT_VIDEO_ID_RE = re.compile(r"^[0-9A-Za-z_-]{11}$")
HA_ENTITY_ID_RE = re.compile(r"^[a-z0-9_]+\.[a-z0-9_]+$")


def _resolve_env_secrets(data):
    if isinstance(data, dict):
        resolved = {}
        for k, v in data.items():
            if isinstance(v, (dict, list)):
                resolved[k] = _resolve_env_secrets(v)
            else:
                resolved[k] = v
        for k, v in list(data.items()):
            if k.endswith("_env") and isinstance(v, str):
                base_key = k[:-4]
                if base_key not in resolved or not resolved[base_key]:
                    resolved[base_key] = os.environ.get(v.strip(), "")
        return resolved
    elif isinstance(data, list):
        return [_resolve_env_secrets(item) for item in data]
    return data


_CONFIG_CACHE = None
_CONFIG_CACHE_TIME = 0
_CONFIG_LOCK = threading.Lock()


def _load_config():
    global _CONFIG_CACHE, _CONFIG_CACHE_TIME
    now = time.time()
    with _CONFIG_LOCK:
        if _CONFIG_CACHE is not None and now - _CONFIG_CACHE_TIME < 5:
            return _CONFIG_CACHE

    candidates = [
        os.environ.get("MIRRORMERE_CONFIG", ""),
        os.environ.get("HEARTH_CONFIG_FILE", ""),
        "/config/config.yaml",
        "/etc/mirrormere/config.yaml",
        "deploy/examples/config.yaml",
        "config.yaml",
        "/secrets/hearth_env.json",
        "/secrets/env.json",
    ]
    raw_cfg = {}
    for path in candidates:
        if path and os.path.exists(path):
            try:
                with open(path, "r", encoding="utf-8") as f:
                    content = f.read()
                if path.endswith((".yaml", ".yml")) or "---" in content or ":" in content:
                    if yaml is not None:
                        parsed = yaml.safe_load(content)
                        if isinstance(parsed, dict):
                            raw_cfg = parsed
                            break
                parsed = json.loads(content)
                if isinstance(parsed, dict):
                    raw_cfg = parsed
                    break
            except Exception:
                pass

    resolved = _resolve_env_secrets(raw_cfg)
    with _CONFIG_LOCK:
        _CONFIG_CACHE = resolved
        _CONFIG_CACHE_TIME = now
    return resolved


def _cfg(key, default=""):
    env_val = os.environ.get(key)
    if env_val is not None and env_val != "":
        return env_val

    env_secret_name = os.environ.get(key + "_ENV")
    if env_secret_name and os.environ.get(env_secret_name):
        return os.environ.get(env_secret_name)

    cfg = _load_config()
    if key in cfg and cfg[key] is not None and cfg[key] != "":
        return cfg[key]
    k_lower = key.lower()
    if k_lower in cfg and cfg[k_lower] is not None and cfg[k_lower] != "":
        return cfg[k_lower]

    for env_k in (key + "_env", k_lower + "_env"):
        if env_k in cfg and isinstance(cfg[env_k], str) and cfg[env_k].strip():
            val = os.environ.get(cfg[env_k].strip())
            if val is not None and val != "":
                return val

    for section_name in ("bridge", "hearth", "ical_bridge", "media", "display"):
        sec = cfg.get(section_name)
        if isinstance(sec, dict):
            if key in sec and sec[key] is not None and sec[key] != "":
                return sec[key]
            if k_lower in sec and sec[k_lower] is not None and sec[k_lower] != "":
                return sec[k_lower]
            for env_k in (key + "_env", k_lower + "_env"):
                if env_k in sec and isinstance(sec[env_k], str) and sec[env_k].strip():
                    val = os.environ.get(sec[env_k].strip())
                    if val is not None and val != "":
                        return val

    return default


def _img_cache_get(key):
    now = time.time()
    with IMG_CACHE_LOCK:
        entry = IMG_CACHE.get(key)
        if entry and now - entry[0] < IMG_CACHE_TTL:
            IMG_CACHE.move_to_end(key)
            return entry[1], entry[2]
    return None, None


def _img_cache_set(key, data, ctype):
    now = time.time()
    with IMG_CACHE_LOCK:
        IMG_CACHE[key] = (now, data, ctype)
        IMG_CACHE.move_to_end(key)
        while len(IMG_CACHE) > MAX_IMG_CACHE_ENTRIES:
            IMG_CACHE.popitem(last=False)


_DOC_NETS = (
    ipaddress.ip_network("192.0.2.0/24"),
    ipaddress.ip_network("198.51.100.0/24"),
    ipaddress.ip_network("203.0.113.0/24"),
)


def _is_internal_caller(client_ip):
    try:
        addr = ipaddress.ip_address(client_ip)
        if addr.is_loopback:
            return True
        for net in _DOC_NETS:
            if addr in net:
                return False
        if addr.is_private:
            return True
    except Exception:
        pass
    return False


def _is_valid_album_url(url):
    if not url or not isinstance(url, str):
        return False
    try:
        parsed = urllib.parse.urlparse(url.strip())
        if parsed.scheme.lower() != "https":
            return False
        hostname = (parsed.hostname or "").lower()
        if hostname in ("photos.app.goo.gl", "photos.google.com"):
            return True
    except Exception:
        pass
    return False


def _get_lock(key):
    with FETCH_LOCKS_GUARD:
        if key not in FETCH_LOCKS:
            FETCH_LOCKS[key] = threading.Lock()
        return FETCH_LOCKS[key]


def get_google_token():
    with TOKEN_LOCK:
        now = time.time()
        if TOKEN_CACHE["token"] and now < TOKEN_CACHE["exp"] - 60:
            return TOKEN_CACHE["token"]
        oauth_path = os.environ.get("GOOGLE_OAUTH_FILE", "/secrets/google_oauth.json")
        with open(oauth_path) as f:
            oauth = json.load(f)
        data = urllib.parse.urlencode({
            "client_id": oauth["client_id"],
            "client_secret": oauth["client_secret"],
            "refresh_token": oauth["refresh_token"],
            "grant_type": "refresh_token",
        }).encode()
        req = urllib.request.Request("https://oauth2.googleapis.com/token", data=data)
        with urllib.request.urlopen(req, timeout=10) as r:
            resp = json.loads(r.read().decode())
        TOKEN_CACHE["token"] = resp["access_token"]
        TOKEN_CACHE["exp"] = now + int(resp.get("expires_in", 3500))
        return TOKEN_CACHE["token"]


def fetch_gcal_events(cal_id):
    if not cal_id:
        return []
    key = f"gcal:{cal_id}"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
            return CACHE[key][1]
    with _get_lock(key):
        with CACHE_LOCK:
            now = time.time()
            if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
                return CACHE[key][1]
        items = []
        try:
            token = get_google_token()
            tmin = (datetime.datetime.now(UTC) - datetime.timedelta(days=45)).strftime("%Y-%m-%dT00:00:00Z")
            tmax = (datetime.datetime.now(UTC) + datetime.timedelta(days=120)).strftime("%Y-%m-%dT00:00:00Z")
            q = urllib.parse.urlencode({
                "singleEvents": "true",
                "orderBy": "startTime",
                "maxResults": "500",
                "timeMin": tmin,
                "timeMax": tmax,
            })
            url = f"https://www.googleapis.com/calendar/v3/calendars/{urllib.parse.quote(cal_id)}/events?{q}"
            req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
            with urllib.request.urlopen(req, timeout=6) as r:
                items = json.loads(r.read().decode()).get("items", [])
        except Exception as e:
            print(f"[HEARTH-Bridge] Warning fetching {cal_id}: {e}", flush=True)
            with CACHE_LOCK:
                items = CACHE.get(key, (0, []))[1]
        with CACHE_LOCK:
            CACHE[key] = (now, items)
        return items


def fetch_mealie_recipe_detail(slug, base, tok):
    if not slug:
        return {}
    key = f"mealie_recipe:{slug}"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < IMG_CACHE_TTL:
            return CACHE[key][1]
    try:
        req = urllib.request.Request(
            f"{base}/api/recipes/{urllib.parse.quote(slug)}",
            headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=5) as r:
            detail = json.loads(r.read().decode())
        with CACHE_LOCK:
            CACHE[key] = (now, detail)
        return detail
    except Exception as e:
        print(f"[HEARTH-Bridge] Recipe detail error for {slug}: {e}", flush=True)
        with CACHE_LOCK:
            return CACHE.get(key, (0, {}))[1]


def fetch_mealie_events():
    key = "mealie"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
            return CACHE[key][1]
    with _get_lock(key):
        with CACHE_LOCK:
            now = time.time()
            if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
                return CACHE[key][1]
        base = str(_cfg("MEALIE_BASE_URL", "")).rstrip("/")
        tok = str(_cfg("MEALIE_API_TOKEN", ""))
        if not base or not tok:
            return []
        url = f"{base}/api/households/mealplans?perPage=60"
        req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}"})
        try:
            with urllib.request.urlopen(req, timeout=8) as r:
                items = json.loads(r.read().decode()).get("items", [])
            slugs = sorted({
                (item.get("recipe") or {}).get("slug")
                for item in items
                if (item.get("recipe") or {}).get("slug")
            })
            if slugs:
                with ThreadPoolExecutor(max_workers=6) as pool:
                    detail_map = dict(
                        zip(slugs, pool.map(lambda s: fetch_mealie_recipe_detail(s, base, tok), slugs))
                    )
                for item in items:
                    slug = (item.get("recipe") or {}).get("slug")
                    if slug and slug in detail_map:
                        item["_detail"] = detail_map[slug]
        except Exception as e:
            print(f"[HEARTH-Bridge] Error fetching Mealie: {e}", flush=True)
            with CACHE_LOCK:
                items = CACHE.get(key, (0, []))[1]
        with CACHE_LOCK:
            CACHE[key] = (now, items)
        return items


def fetch_mealie_image(recipe_id):
    if not recipe_id or not RECIPE_ID_RE.match(recipe_id):
        return None, None
    key = f"mimg:{recipe_id}"
    cached_data, cached_type = _img_cache_get(key)
    if cached_data:
        return cached_data, cached_type
    base = str(_cfg("MEALIE_BASE_URL", "")).rstrip("/")
    if not base:
        return None, None
    for img_name in ("small-original.webp", "min-original.webp", "original.webp"):
        url = f"{base}/api/media/recipes/{urllib.parse.quote(recipe_id)}/images/{img_name}"
        try:
            req = urllib.request.Request(url)
            with urllib.request.urlopen(req, timeout=6) as r:
                data = r.read()
                ctype = r.headers.get("Content-Type", "image/webp")
                if data:
                    _img_cache_set(key, data, ctype)
                    return data, ctype
        except Exception:
            continue
    return None, None


def fetch_plex_recent():
    key = "plex:recent"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < 180:
            return CACHE[key][1]
    with _get_lock(key):
        with CACHE_LOCK:
            now = time.time()
            if key in CACHE and now - CACHE[key][0] < 180:
                return CACHE[key][1]
        plex_base = str(_cfg("PLEX_BASE_URL", "")).rstrip("/")
        plex_token = str(_cfg("PLEX_TOKEN", ""))
        if not plex_base or not plex_token:
            return []
        out = []
        try:
            url = f"{plex_base}/library/recentlyAdded?X-Plex-Container-Start=0&X-Plex-Container-Size=24"
            req = urllib.request.Request(
                url,
                headers={"X-Plex-Token": plex_token, "Accept": "application/json"},
            )
            with urllib.request.urlopen(req, timeout=8) as r:
                data = json.loads(r.read().decode())
            items = data.get("MediaContainer", {}).get("Metadata", [])
            for m in items:
                mtype = m.get("type", "")
                title = m.get("title", "Untitled")
                subtitle = ""
                if mtype == "season":
                    title = m.get("parentTitle") or title
                    subtitle = m.get("title") or f"Season {m.get('index', '')}"
                elif mtype == "episode":
                    title = m.get("grandparentTitle") or title
                    s_idx = m.get("parentIndex")
                    e_idx = m.get("index")
                    ep_name = m.get("title", "")
                    if s_idx is not None and e_idx is not None:
                        subtitle = f"S{int(s_idx):02d}E{int(e_idx):02d} · {ep_name}"
                    else:
                        subtitle = ep_name
                elif mtype == "album":
                    subtitle = m.get("parentTitle") or "Audiobook"
                else:
                    subtitle = str(m.get("year") or "")

                thumb_path = m.get("thumb") or m.get("parentThumb") or m.get("grandparentThumb") or ""
                thumb_url = f"/hearth-api/plex/thumb?path={urllib.parse.quote(thumb_path)}" if thumb_path else ""
                added_at = int(m.get("addedAt") or 0)
                category = "movie"
                lib_title = (m.get("librarySectionTitle") or "").lower()
                if "audiobook" in lib_title or mtype == "album":
                    category = "audiobook"
                elif mtype in ("show", "season", "episode") or "tv" in lib_title:
                    category = "tv"

                out.append({
                    "id": str(m.get("ratingKey", "")),
                    "title": title,
                    "subtitle": subtitle,
                    "type": mtype,
                    "category": category,
                    "year": m.get("year"),
                    "rating": m.get("audienceRating") or m.get("rating"),
                    "summary": (m.get("summary") or "")[:280],
                    "thumb": thumb_url,
                    "added_at": added_at,
                })
        except Exception as e:
            print(f"[HEARTH-Bridge] Error fetching Plex recent: {e}", flush=True)
            with CACHE_LOCK:
                out = CACHE.get(key, (0, []))[1]
        with CACHE_LOCK:
            CACHE[key] = (now, out)
        return out


def fetch_plex_thumb(thumb_path):
    if not thumb_path:
        return None, None
    clean_path = thumb_path.split("?", 1)[0]
    if ".." in clean_path or not PLEX_THUMB_RE.match(clean_path):
        return None, None
    key = f"pthumb:{clean_path}"
    cached_data, cached_type = _img_cache_get(key)
    if cached_data:
        return cached_data, cached_type
    plex_base = str(_cfg("PLEX_BASE_URL", "")).rstrip("/")
    plex_token = str(_cfg("PLEX_TOKEN", ""))
    if not plex_base or not plex_token:
        return None, None
    url = f"{plex_base}{clean_path}?X-Plex-Token={urllib.parse.quote(plex_token)}"
    try:
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=6) as r:
            data = r.read()
            ctype = r.headers.get("Content-Type", "image/jpeg")
            if data:
                _img_cache_set(key, data, ctype)
                return data, ctype
    except Exception as e:
        print(f"[HEARTH-Bridge] Error fetching Plex thumb {clean_path}: {e}", flush=True)
    return None, None


def fetch_arr_upcoming():
    key = "arr:upcoming"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < 300:
            return CACHE[key][1]
    with _get_lock(key):
        with CACHE_LOCK:
            now = time.time()
            if key in CACHE and now - CACHE[key][0] < 300:
                return CACHE[key][1]
        today = datetime.datetime.now(PT).date()
        end_d = today + datetime.timedelta(days=14)
        start_s = today.isoformat()
        end_s = end_d.isoformat()
        items = []

        # 1. Sonarr upcoming episodes
        s_base = str(_cfg("SONARR_BASE_URL", "")).rstrip("/")
        s_key = str(_cfg("SONARR_API_KEY", ""))
        if s_base and s_key:
            try:
                url = f"{s_base}/api/v3/calendar?start={start_s}&end={end_s}&includeSeries=true"
                req = urllib.request.Request(url, headers={"X-Api-Key": s_key, "Accept": "application/json"})
                with urllib.request.urlopen(req, timeout=8) as r:
                    eps = json.loads(r.read().decode())
                for ep in eps:
                    series = ep.get("series") or {}
                    stitle = series.get("title") or ep.get("title") or "TV Episode"
                    s_num = ep.get("seasonNumber", 0)
                    e_num = ep.get("episodeNumber", 0)
                    air_utc = ep.get("airDateUtc") or ep.get("airDate") or ""
                    air_label = ep.get("airDate") or ""
                    sort_ts = 0
                    if air_utc and "T" in air_utc:
                        try:
                            dt = datetime.datetime.fromisoformat(air_utc.replace("Z", "+00:00")).astimezone(PT)
                            sort_ts = int(dt.timestamp())
                            if dt.date() == today:
                                air_label = f"Tonight · {dt.strftime('%-I:%M %p')}"
                            elif dt.date() == today + datetime.timedelta(days=1):
                                air_label = f"Tomorrow · {dt.strftime('%-I:%M %p')}"
                            else:
                                air_label = dt.strftime("%a %b %-d")
                        except Exception:
                            pass
                    poster = ""
                    for img in (series.get("images") or []):
                        if img.get("coverType") == "poster" and img.get("remoteUrl"):
                            poster = img["remoteUrl"]
                            break
                    items.append({
                        "id": f"sonarr_{ep.get('id')}",
                        "title": stitle,
                        "subtitle": f"S{int(s_num):02d}E{int(e_num):02d} · {air_label}",
                        "badge": "AIRING",
                        "category": "upcoming",
                        "thumb": poster,
                        "sort_ts": sort_ts,
                        "summary": (ep.get("overview") or series.get("overview") or "")[:240],
                    })
            except Exception as e:
                print(f"[HEARTH-Bridge] Sonarr calendar error: {e}", flush=True)

        # 2. Radarr upcoming movies
        r_base = str(_cfg("RADARR_BASE_URL", "")).rstrip("/")
        r_key = str(_cfg("RADARR_API_KEY", ""))
        if r_base and r_key:
            try:
                url = f"{r_base}/api/v3/calendar?start={start_s}&end={end_s}"
                req = urllib.request.Request(url, headers={"X-Api-Key": r_key, "Accept": "application/json"})
                with urllib.request.urlopen(req, timeout=8) as r:
                    movies = json.loads(r.read().decode())
                for mv in movies:
                    mtitle = mv.get("title") or "Movie"
                    rel_raw = mv.get("digitalRelease") or mv.get("physicalRelease") or mv.get("inCinemas") or ""
                    rel_label = rel_raw[:10]
                    sort_ts = 0
                    if rel_raw:
                        try:
                            dt = datetime.datetime.fromisoformat(rel_raw.replace("Z", "+00:00")).astimezone(PT)
                            sort_ts = int(dt.timestamp())
                            rel_label = dt.strftime("%b %-d")
                        except Exception:
                            pass
                    poster = ""
                    for img in (mv.get("images") or []):
                        if img.get("coverType") == "poster" and img.get("remoteUrl"):
                            poster = img["remoteUrl"]
                            break
                    items.append({
                        "id": f"radarr_{mv.get('id')}",
                        "title": mtitle,
                        "subtitle": f"Drops {rel_label}",
                        "badge": "SOON",
                        "category": "upcoming",
                        "thumb": poster,
                        "sort_ts": sort_ts,
                        "summary": (mv.get("overview") or "")[:240],
                    })
            except Exception as e:
                print(f"[HEARTH-Bridge] Radarr calendar error: {e}", flush=True)

        items.sort(key=lambda x: x.get("sort_ts") or 9999999999)
        with CACHE_LOCK:
            CACHE[key] = (now, items)
        return items


def seerr_search(query):
    if not query or not query.strip():
        return []
    base = str(_cfg("SEERR_BASE_URL", "")).rstrip("/")
    key = str(_cfg("SEERR_API_KEY", ""))
    if not base or not key:
        return []
    url = f"{base}/api/v1/search?query={urllib.parse.quote(query)}"
    req = urllib.request.Request(url, headers={"X-Api-Key": key, "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            data = json.loads(r.read().decode())
        out = []
        for r_item in (data.get("results") or [])[:12]:
            mtype = r_item.get("mediaType", "")
            if mtype not in ("movie", "tv"):
                continue
            title = r_item.get("title") or r_item.get("name") or "Untitled"
            year = (r_item.get("releaseDate") or r_item.get("firstAirDate") or "")[:4]
            poster_path = r_item.get("posterPath") or ""
            poster = f"https://image.tmdb.org/t/p/w300{poster_path}" if poster_path else ""
            m_info = r_item.get("mediaInfo") or {}
            status_code = m_info.get("status", 1)
            # Seerr status codes: 1=unknown/unrequested, 2=pending, 3=processing, 4=partially available, 5=available
            status_label = "available" if status_code == 5 else ("requested" if status_code in (2, 3, 4) else "")
            item_id = r_item.get("id")
            out.append({
                "id": item_id,
                "tmdb_id": item_id,
                "media_type": mtype,
                "title": title,
                "year": year,
                "summary": (r_item.get("overview") or "")[:220],
                "thumb": poster,
                "status": status_label,
                "rating": r_item.get("voteAverage"),
            })
        return out
    except Exception as e:
        print(f"[HEARTH-Bridge] Seerr search error: {e}", flush=True)
        return []


def seerr_request(media_type, tmdb_id):
    if media_type not in ("movie", "tv"):
        return False, {"error": "Invalid media_type"}
    base = str(_cfg("SEERR_BASE_URL", "")).rstrip("/")
    key = str(_cfg("SEERR_API_KEY", ""))
    if not base or not key:
        return False, {"error": "Seerr not configured"}
    try:
        media_id = int(tmdb_id)
    except (ValueError, TypeError):
        return False, {"error": "Invalid tmdb_id"}
    body = {"mediaType": media_type, "mediaId": media_id}
    if media_type == "tv":
        body["seasons"] = "all"
    payload = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        f"{base}/api/v1/request",
        data=payload,
        method="POST",
        headers={"X-Api-Key": key, "Content-Type": "application/json", "Accept": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return True, json.loads(r.read().decode())
    except Exception as e:
        print(f"[HEARTH-Bridge] Seerr request error: {e}", flush=True)
        return False, {"error": str(e)}



# === PLEX LIVING ROOM TV CAST & YOUTUBE MUSIC / GOOGLE HOME CONTROLLER ===
import subprocess
import shutil

PACKAGES_DIR = "/app/.packages"
if os.path.isdir(PACKAGES_DIR) and PACKAGES_DIR not in sys.path:
    sys.path.insert(0, PACKAGES_DIR)

YT_TOKEN_LOCK = threading.Lock()
YT_TOKEN_CACHE = {"token": None, "exp": 0}
STREAM_URL_CACHE = {}
STREAM_URL_LOCK = threading.Lock()

MUSIC_STATE_LOCK = threading.Lock()
MUSIC_STATE = {
    "active_speaker": "",
    "queue": [],
    "queue_index": 0,
    "playlist_title": "",
    "shuffle": True,
    "last_cast_ts": 0,
    "playing_video_id": "",
}
MUSIC_STOP_EVENT = threading.Event()


def _get_speakers():
    cfg = _load_config()
    spks = cfg.get("speakers")
    if spks is None:
        spks = cfg.get("music_speakers")
    if spks is None and isinstance(cfg.get("bridge"), dict):
        spks = cfg.get("bridge", {}).get("speakers")
    if isinstance(spks, list):
        return spks
    return []


SPEAKER_LIST = []


def _ha_request(method, path, payload=None, timeout=8):
    ha_url = str(_cfg("HA_BASE_URL", "")).rstrip("/")
    ha_tok = str(_cfg("HA_ACCESS_TOKEN", ""))
    if not ha_url or not ha_tok:
        return None
    url = f"{ha_url}{path}"
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(
        url,
        data=data,
        method=method,
        headers={
            "Authorization": f"Bearer {ha_tok}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read().decode("utf-8")
            return json.loads(raw) if raw.strip() else {}
    except Exception as e:
        print(f"[HEARTH-Bridge] HA request error ({method} {path}): {e}", flush=True)
        return None


def get_yt_oauth_token():
    with YT_TOKEN_LOCK:
        now = time.time()
        if YT_TOKEN_CACHE["token"] and now < YT_TOKEN_CACHE["exp"] - 60:
            return YT_TOKEN_CACHE["token"]
        raw = _cfg("YT_MUSIC_OAUTH", {})
        oauth = {}
        if isinstance(raw, str) and raw.strip():
            try:
                oauth = json.loads(raw)
            except Exception:
                pass
        elif isinstance(raw, dict):
            oauth = raw
        if not oauth.get("client_id") or not oauth.get("refresh_token"):
            return None
        data = urllib.parse.urlencode({
            "client_id": oauth["client_id"],
            "client_secret": oauth.get("client_secret", ""),
            "refresh_token": oauth["refresh_token"],
            "grant_type": "refresh_token",
        }).encode()
        try:
            req = urllib.request.Request("https://oauth2.googleapis.com/token", data=data)
            with urllib.request.urlopen(req, timeout=10) as r:
                resp = json.loads(r.read().decode())
            YT_TOKEN_CACHE["token"] = resp["access_token"]
            YT_TOKEN_CACHE["exp"] = now + int(resp.get("expires_in", 3500))
            return YT_TOKEN_CACHE["token"]
        except Exception as e:
            print(f"[HEARTH-Bridge] Error refreshing YT OAuth token: {e}", flush=True)
            return None


def fetch_yt_playlists():
    key = "yt:playlists"
    now = time.time()
    with CACHE_LOCK:
        if key in CACHE and now - CACHE[key][0] < 600:
            return CACHE[key][1]
    tok = get_yt_oauth_token()
    if not tok:
        return []
    url = "https://www.googleapis.com/youtube/v3/playlists?part=snippet,contentDetails&mine=true&maxResults=25"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"})
    out = []
    try:
        with urllib.request.urlopen(req, timeout=8) as r:
            data = json.loads(r.read().decode())
        for p in data.get("items", []):
            snip = p.get("snippet") or {}
            thumbs = snip.get("thumbnails") or {}
            thumb_url = (
                (thumbs.get("high") or {}).get("url")
                or (thumbs.get("medium") or {}).get("url")
                or (thumbs.get("default") or {}).get("url")
                or ""
            )
            count = (p.get("contentDetails") or {}).get("itemCount", 0)
            out.append({
                "playlist_id": p.get("id", ""),
                "title": snip.get("title", "Playlist"),
                "count": count,
                "thumb": thumb_url,
            })
        with CACHE_LOCK:
            CACHE[key] = (now, out)
    except Exception as e:
        print(f"[HEARTH-Bridge] YT playlists error: {e}", flush=True)
        with CACHE_LOCK:
            out = CACHE.get(key, (0, []))[1]
    return out


def fetch_yt_playlist_tracks(playlist_id):
    tok = get_yt_oauth_token()
    if not tok or not playlist_id:
        return []
    url = (
        f"https://www.googleapis.com/youtube/v3/playlistItems?"
        f"part=snippet,contentDetails&playlistId={urllib.parse.quote(playlist_id)}&maxResults=50"
    )
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"})
    tracks = []
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            data = json.loads(r.read().decode())
        for item in data.get("items", []):
            snip = item.get("snippet") or {}
            vid = (item.get("contentDetails") or {}).get("videoId") or (snip.get("resourceId") or {}).get("videoId")
            if not vid:
                continue
            thumbs = snip.get("thumbnails") or {}
            thumb = (
                (thumbs.get("high") or {}).get("url")
                or (thumbs.get("medium") or {}).get("url")
                or (thumbs.get("default") or {}).get("url")
                or f"https://i.ytimg.com/vi/{vid}/hqdefault.jpg"
            )
            tracks.append({
                "video_id": vid,
                "title": snip.get("title", "Track"),
                "artist": snip.get("videoOwnerChannelTitle", "").replace(" - Topic", ""),
                "thumb": thumb,
            })
    except Exception as e:
        print(f"[HEARTH-Bridge] Error fetching playlist tracks: {e}", flush=True)
    return tracks


def search_yt_music(query):
    if not query or not query.strip():
        return []
    try:
        import ytmusicapi
        ytm = ytmusicapi.YTMusic()
        res = ytm.search(query, filter="songs", limit=12)
        out = []
        for r in res:
            vid = r.get("videoId")
            if not vid:
                continue
            arts = ", ".join(a.get("name", "") for a in (r.get("artists") or []) if a.get("name"))
            thumbs = r.get("thumbnails") or []
            thumb = thumbs[-1].get("url", "") if thumbs else f"https://i.ytimg.com/vi/{vid}/hqdefault.jpg"
            out.append({
                "video_id": vid,
                "title": r.get("title", "Track"),
                "artist": arts or "Unknown Artist",
                "album": (r.get("album") or {}).get("name", ""),
                "duration": r.get("duration", ""),
                "thumb": thumb,
            })
        return out
    except Exception as e:
        print(f"[HEARTH-Bridge] YTMusic search error: {e}", flush=True)
        return []


def resolve_yt_audio_stream(video_id):
    if not video_id or not isinstance(video_id, str) or not YT_VIDEO_ID_RE.match(video_id):
        return None, None
    now = time.time()
    with STREAM_URL_LOCK:
        cached = STREAM_URL_CACHE.get(video_id)
        if cached and now - cached[0] < 1800:
            return cached[1], cached[2]
    try:
        import yt_dlp
        ydl_opts = {
            "format": "bestaudio[ext=m4a]/bestaudio/best",
            "quiet": True,
            "no_warnings": True,
            "noplaylist": True,
            "skip_download": True,
        }
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            info = ydl.extract_info(f"https://www.youtube.com/watch?v={video_id}", download=False)
            url = info.get("url")
            ext = info.get("ext") or "mp4"
            ctype = "audio/mp4" if ext == "m4a" else ("audio/webm" if ext == "webm" else "audio/mpeg")
            if url:
                with STREAM_URL_LOCK:
                    STREAM_URL_CACHE[video_id] = (now, url, ctype)
                return url, ctype
    except Exception as e:
        print(f"[HEARTH-Bridge] Stream resolution error for {video_id}: {e}", flush=True)
    return None, None


def _cast_track_to_speaker(speaker, track):
    if not speaker or not track or not track.get("video_id"):
        return False
    if not isinstance(speaker, str) or not HA_ENTITY_ID_RE.match(speaker):
        return False
    vid = track["video_id"]
    if not isinstance(vid, str) or not YT_VIDEO_ID_RE.match(vid):
        return False
    lan_url = str(_cfg("BRIDGE_LAN_URL", "http://127.0.0.1:8099")).rstrip("/")
    media_url = f"{lan_url}/hearth-api/music/stream/{vid}.m4a"
    payload = {
        "entity_id": speaker,
        "media_content_id": media_url,
        "media_content_type": "music",
        "extra": {
            "metadata": {
                "metadataType": 3,
                "title": track.get("title") or "YouTube Music",
                "artist": track.get("artist") or "",
                "images": [{"url": track.get("thumb", "")}] if track.get("thumb") else [],
            },
        },
    }
    with MUSIC_STATE_LOCK:
        MUSIC_STATE["playing_video_id"] = vid
        MUSIC_STATE["last_cast_ts"] = time.time()
    res = _ha_request("POST", "/api/services/media_player/play_media", payload, timeout=10)
    return res is not None


def get_music_dashboard_state():
    speakers = _get_speakers()
    with MUSIC_STATE_LOCK:
        active_spk = MUSIC_STATE["active_speaker"]
        if not active_spk and speakers:
            active_spk = speakers[0].get("entity_id", "")
            MUSIC_STATE["active_speaker"] = active_spk
        queue = list(MUSIC_STATE["queue"])
        q_idx = MUSIC_STATE["queue_index"]
        pl_title = MUSIC_STATE["playlist_title"]
        shuffle = MUSIC_STATE["shuffle"]

    spk_st = {}
    if active_spk and HA_ENTITY_ID_RE.match(active_spk):
        spk_st = _ha_request("GET", f"/api/states/{active_spk}", timeout=5) or {}
    attrs = spk_st.get("attributes") or {}
    state_str = spk_st.get("state", "off")
    vol_pct = int(round(float(attrs.get("volume_level", 0.35)) * 100))

    cur_track = queue[q_idx] if 0 <= q_idx < len(queue) else {}
    now_playing = {
        "state": state_str,
        "title": attrs.get("media_title") or cur_track.get("title") or "Ready to Play",
        "artist": attrs.get("media_artist") or cur_track.get("artist") or "Select a playlist or search YouTube Music",
        "thumb": attrs.get("entity_picture") or cur_track.get("thumb") or "",
        "volume": vol_pct,
        "playlist_title": pl_title,
        "queue_index": q_idx,
        "queue_length": len(queue),
        "shuffle": shuffle,
    }

    return {
        "active_speaker": active_spk,
        "speakers": speakers,
        "now_playing": now_playing,
        "queue": queue[:20],
        "playlists": fetch_yt_playlists(),
    }


def start_music_playback(body):
    import random
    speaker = body.get("speaker")
    video_id = body.get("video_id")
    playlist_id = body.get("playlist_id")
    playlist_title = body.get("playlist_title") or ""
    shuffle = bool(body.get("shuffle", True))

    with MUSIC_STATE_LOCK:
        if speaker:
            MUSIC_STATE["active_speaker"] = speaker
        active_spk = MUSIC_STATE["active_speaker"]
        if not active_spk and _get_speakers():
            active_spk = _get_speakers()[0].get("entity_id", "")
            MUSIC_STATE["active_speaker"] = active_spk
        MUSIC_STATE["shuffle"] = shuffle

    tracks = []
    if playlist_id:
        tracks = fetch_yt_playlist_tracks(playlist_id)
        if shuffle and len(tracks) > 1:
            random.shuffle(tracks)
    elif video_id:
        first_track = {
            "video_id": video_id,
            "title": body.get("title") or "YouTube Music",
            "artist": body.get("artist") or "",
            "thumb": body.get("thumb") or f"https://i.ytimg.com/vi/{video_id}/hqdefault.jpg",
        }
        tracks = [first_track]
        try:
            import ytmusicapi
            ytm = ytmusicapi.YTMusic()
            watch = ytm.get_watch_playlist(videoId=video_id, limit=20)
            for wt in (watch.get("tracks") or []):
                wvid = wt.get("videoId")
                if not wvid or wvid == video_id:
                    continue
                warts = ", ".join(a.get("name", "") for a in (wt.get("artists") or []) if a.get("name"))
                wthumbs = wt.get("thumbnail") or []
                wthumb = wthumbs[-1].get("url", "") if wthumbs else f"https://i.ytimg.com/vi/{wvid}/hqdefault.jpg"
                tracks.append({
                    "video_id": wvid,
                    "title": wt.get("title") or "Track",
                    "artist": warts or "YouTube Music",
                    "thumb": wthumb,
                })
        except Exception as e:
            print(f"[HEARTH-Bridge] watch playlist warning: {e}", flush=True)

    if not tracks:
        return False, {"error": "No playable tracks found"}

    with MUSIC_STATE_LOCK:
        MUSIC_STATE["queue"] = tracks
        MUSIC_STATE["queue_index"] = 0
        MUSIC_STATE["playlist_title"] = playlist_title or (tracks[0].get("title") + " Radio")

    ok = _cast_track_to_speaker(active_spk, tracks[0])
    return ok, get_music_dashboard_state()


def control_music_playback(body):
    action = body.get("action", "")
    speaker = body.get("speaker")
    with MUSIC_STATE_LOCK:
        if speaker:
            MUSIC_STATE["active_speaker"] = speaker
        active_spk = MUSIC_STATE["active_speaker"]
        queue = list(MUSIC_STATE["queue"])
        q_idx = MUSIC_STATE["queue_index"]

    if action == "select_speaker":
        return True, get_music_dashboard_state()
    elif action == "play_pause" and active_spk:
        _ha_request("POST", "/api/services/media_player/media_play_pause", {"entity_id": active_spk})
    elif action == "stop" and active_spk:
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["playing_video_id"] = ""
        _ha_request("POST", "/api/services/media_player/media_stop", {"entity_id": active_spk})
    elif action == "volume" and active_spk:
        try:
            val = float(body.get("value", 35))
        except (ValueError, TypeError):
            val = 35.0
        vol = max(0.0, min(1.0, val / 100.0))
        _ha_request("POST", "/api/services/media_player/volume_set", {"entity_id": active_spk, "volume_level": vol})
    elif action == "next" and queue and active_spk:
        next_idx = (q_idx + 1) % len(queue)
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["queue_index"] = next_idx
        _cast_track_to_speaker(active_spk, queue[next_idx])
    elif action == "prev" and queue and active_spk:
        prev_idx = (q_idx - 1) % len(queue)
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["queue_index"] = prev_idx
        _cast_track_to_speaker(active_spk, queue[prev_idx])
    elif action == "play_index" and queue and active_spk:
        try:
            idx = int(body.get("index", 0))
        except (ValueError, TypeError):
            idx = 0
        if 0 <= idx < len(queue):
            with MUSIC_STATE_LOCK:
                MUSIC_STATE["queue_index"] = idx
            _cast_track_to_speaker(active_spk, queue[idx])

    # Readiness polling instead of arbitrary time.sleep(0.35)
    if active_spk and HA_ENTITY_ID_RE.match(active_spk):
        deadline = time.time() + 0.5
        while time.time() < deadline:
            st = _ha_request("GET", f"/api/states/{active_spk}", timeout=1) or {}
            if st.get("state") in ("playing", "paused", "idle"):
                break
            time.sleep(0.05)
    return True, get_music_dashboard_state()


def _music_auto_advance_loop():
    while not MUSIC_STOP_EVENT.is_set():
        if MUSIC_STOP_EVENT.wait(5):
            break
        try:
            with MUSIC_STATE_LOCK:
                vid = MUSIC_STATE["playing_video_id"]
                last_ts = MUSIC_STATE["last_cast_ts"]
                active_spk = MUSIC_STATE["active_speaker"]
                queue = list(MUSIC_STATE["queue"])
                q_idx = MUSIC_STATE["queue_index"]

            if not vid or not active_spk or not queue or time.time() - last_ts < 20:
                continue

            if not HA_ENTITY_ID_RE.match(active_spk):
                continue

            st = _ha_request("GET", f"/api/states/{active_spk}", timeout=4) or {}
            state_str = st.get("state")
            if state_str == "idle" and q_idx + 1 < len(queue):
                next_idx = q_idx + 1
                with MUSIC_STATE_LOCK:
                    MUSIC_STATE["queue_index"] = next_idx
                _cast_track_to_speaker(active_spk, queue[next_idx])
        except Exception as e:
            print(f"[HEARTH-Bridge] auto-advance loop warning: {e}", flush=True)


def launch_plex_on_living_room_tv(body):
    rating_key = str(body.get("rating_key") or body.get("id") or "").strip()
    mtype = str(body.get("type") or "movie").lower()
    tv_remote = str(_cfg("PLEX_TV_REMOTE_ENTITY", "remote.living_room_tv"))
    tv_media_player = str(_cfg("PLEX_TV_MEDIA_PLAYER_ENTITY", "media_player.living_room_tv"))
    tv_cast_player = str(_cfg("PLEX_TV_CAST_ENTITY", "media_player.living_room_living_room_tv"))

    for ent in (tv_remote, tv_media_player, tv_cast_player):
        if not HA_ENTITY_ID_RE.match(ent):
            return False, {"error": f"Invalid Home Assistant entity ID: {ent}"}

    plex_base = str(_cfg("PLEX_BASE_URL", "")).rstrip("/")
    plex_token = str(_cfg("PLEX_TOKEN", ""))
    if not rating_key or not plex_base or not plex_token:
        return False, {"error": "Missing Plex rating_key or credentials"}

    try:
        # 1. Get Plex machineIdentifier
        req_root = urllib.request.Request(
            f"{plex_base}/",
            headers={"X-Plex-Token": plex_token, "Accept": "application/json"},
        )
        with urllib.request.urlopen(req_root, timeout=6) as r:
            root_data = json.loads(r.read().decode())
        machine_id = str(root_data["MediaContainer"]["machineIdentifier"])

        target_rk = rating_key
        # If a season or show was tapped, resolve the first unwatched episode (or first episode)
        if mtype in ("season", "show"):
            leaves_url = f"{plex_base}/library/metadata/{urllib.parse.quote(rating_key)}/allLeaves"
            req_leaves = urllib.request.Request(
                leaves_url,
                headers={"X-Plex-Token": plex_token, "Accept": "application/json"},
            )
            with urllib.request.urlopen(req_leaves, timeout=6) as r:
                leaves_data = json.loads(r.read().decode())
            leaves = leaves_data.get("MediaContainer", {}).get("Metadata", [])
            if leaves:
                unwatched = [ep for ep in leaves if not ep.get("viewCount")]
                chosen = unwatched[0] if unwatched else leaves[0]
                target_rk = str(chosen.get("ratingKey") or rating_key)

        meta_key = urllib.parse.quote(f"/library/metadata/{target_rk}", safe="")
        deep_link = f"plex://play/?metadataKey={meta_key}&server={machine_id}"

        def _bg_wake_and_play():
            if not (
                HA_ENTITY_ID_RE.match(tv_remote)
                and HA_ENTITY_ID_RE.match(tv_media_player)
                and HA_ENTITY_ID_RE.match(tv_cast_player)
            ):
                return
            rem_st = _ha_request("GET", f"/api/states/{tv_remote}") or {}
            mp_st = _ha_request("GET", f"/api/states/{tv_media_player}") or {}
            is_cold = (
                rem_st.get("state") != "on"
                or mp_st.get("state") in ("off", "standby", "unavailable", None)
            )
            if is_cold:
                _ha_request("POST", "/api/services/remote/turn_on", {"entity_id": tv_remote})
                # Readiness polling: poll remote/mp state until it is "on" up to 10s deadline
                deadline = time.time() + 10.0
                while time.time() < deadline:
                    st = _ha_request("GET", f"/api/states/{tv_remote}", timeout=2) or {}
                    if st.get("state") == "on":
                        break
                    time.sleep(0.5)

            cast_st = _ha_request("GET", f"/api/states/{tv_cast_player}") or {}
            if (
                cast_st.get("state") in ("playing", "paused", "idle")
                and (cast_st.get("attributes") or {}).get("app_id") not in (None, "AndroidNativeApp")
            ):
                _ha_request("POST", "/api/services/media_player/turn_off", {"entity_id": tv_cast_player})
                # Readiness polling: poll until cast state is no longer playing/paused up to 2s
                deadline = time.time() + 2.0
                while time.time() < deadline:
                    st = _ha_request("GET", f"/api/states/{tv_cast_player}", timeout=1) or {}
                    if st.get("state") not in ("playing", "paused"):
                        break
                    time.sleep(0.2)

            _ha_request(
                "POST",
                "/api/services/media_player/play_media",
                {
                    "entity_id": tv_media_player,
                    "media_content_type": "url",
                    "media_content_id": deep_link,
                },
            )
            # Readiness polling: poll until media player state becomes 'playing' or active app is Plex
            deadline = time.time() + 5.0
            while time.time() < deadline:
                st = _ha_request("GET", f"/api/states/{tv_media_player}", timeout=2) or {}
                attrs = st.get("attributes") or {}
                if st.get("state") == "playing" or attrs.get("app_id") in ("com.plexapp.android", "Plex"):
                    break
                time.sleep(0.5)

            _ha_request(
                "POST",
                "/api/services/remote/send_command",
                {"entity_id": tv_remote, "command": "DPAD_CENTER"},
            )

        threading.Thread(target=_bg_wake_and_play, daemon=True).start()
        return True, {"ok": True, "deep_link": deep_link, "rating_key": target_rk}
    except Exception as e:
        print(f"[HEARTH-Bridge] Plex TV launch error: {e}", flush=True)
        return False, {"error": str(e)}


def ics_escape(text):
    if not text:
        return ""
    return (
        str(text)
        .replace("\\", "\\\\")
        .replace(";", "\\;")
        .replace(",", "\\,")
        .replace("\r\n", "\\n")
        .replace("\n", "\\n")
    )


def format_mealie_description(recipe, detail):
    parts = []

    total_time = detail.get("totalTime") or recipe.get("totalTime")
    yield_val = detail.get("recipeYield") or recipe.get("recipeYield")
    tags = [t.get("name", "") for t in (detail.get("tags") or []) if t.get("name")]
    header_bits = []
    if total_time:
        header_bits.append(f"⏱️ {total_time}")
    if yield_val:
        header_bits.append(f"🍽️ Servings: {yield_val}")
    if tags:
        header_bits.append(f"🏷️ {', '.join(tags[:4])}")
    if header_bits:
        parts.append(" · ".join(header_bits))

    desc = (detail.get("description") or recipe.get("description") or "").strip()
    if desc:
        parts.append(desc)

    ings = detail.get("recipeIngredient") or []
    ing_lines = []
    for ing in ings[:18]:
        disp = (ing.get("display") or ing.get("note") or "").strip()
        if disp:
            ing_lines.append(f"• {disp}")
    if ing_lines:
        parts.append("🛒 Ingredients:\n" + "\n".join(ing_lines))

    steps = detail.get("recipeInstructions") or []
    step_lines = []
    for idx, st in enumerate(steps[:12], 1):
        txt = (st.get("text") or "").strip()
        if txt:
            step_lines.append(f"{idx}. {txt}")
    if step_lines:
        parts.append("👩‍🍳 Instructions:\n" + "\n".join(step_lines))

    return "\n\n".join(parts)[:1800]


def gcal_item_to_vevent(ev):
    if ev.get("status") == "cancelled":
        return ""
    uid = ev.get("id") or "unknown"
    summary = ev.get("summary") or "(No Title)"
    location = ev.get("location") or ""
    description = ev.get("description") or ""
    start = ev.get("start") or {}
    end = ev.get("end") or {}

    lines = ["BEGIN:VEVENT", f"UID:{ics_escape(uid)}"]
    if "date" in start:
        s_date = start["date"].replace("-", "")
        e_date = end.get("date", start["date"]).replace("-", "")
        lines.append(f"DTSTART;VALUE=DATE:{s_date}")
        lines.append(f"DTEND;VALUE=DATE:{e_date}")
    elif "dateTime" in start:
        s_dt = datetime.datetime.fromisoformat(start["dateTime"]).astimezone(UTC).strftime("%Y%m%dT%H%M%SZ")
        e_raw = end.get("dateTime") or start["dateTime"]
        e_dt = datetime.datetime.fromisoformat(e_raw).astimezone(UTC).strftime("%Y%m%dT%H%M%SZ")
        lines.append(f"DTSTART:{s_dt}")
        lines.append(f"DTEND:{e_dt}")
    else:
        return ""

    lines.append(f"SUMMARY:{ics_escape(summary)}")
    if location:
        lines.append(f"LOCATION:{ics_escape(location)}")
    if description:
        lines.append(f"DESCRIPTION:{ics_escape(description[:400])}")
    lines.append("END:VEVENT")
    return "\r\n".join(lines)


def classify_family_event(ev):
    text = ((ev.get("summary") or "") + " " + (ev.get("description") or "")).lower()
    owners = set()
    kw_map = _cfg("MEMBER_KEYWORDS", {}) or {}
    for member, keywords in kw_map.items():
        if any(str(k).lower() in text for k in (keywords or [])):
            owners.add(member)
    return owners


def build_feed(feed_name):
    cal_ids = _cfg("CALENDAR_IDS", {}) or {}
    extra_member_cals = _cfg("EXTRA_MEMBER_CALENDARS", {}) or {}
    family_id = cal_ids.get("family", "")
    holiday_id = cal_ids.get("holiday", "en.usa#holiday@group.v.calendar.google.com")

    vevents = []

    if feed_name == "mealie":
        for item in fetch_mealie_events():
            d_str = item.get("date")
            if not d_str:
                continue
            recipe = item.get("recipe") or {}
            detail = item.get("_detail") or {}
            title = recipe.get("name") or item.get("title") or item.get("text")
            if not title:
                continue
            recipe_id = str(recipe.get("id") or detail.get("id") or "")
            has_img = bool(recipe.get("image") or detail.get("image"))
            img_loc = f"/hearth-api/mealie/image/{recipe_id}" if (recipe_id and has_img) else ""
            desc_text = format_mealie_description(recipe, detail)
            d_obj = datetime.date.fromisoformat(d_str)
            next_d = d_obj + datetime.timedelta(days=1)
            uid = f"mealie_{item.get('id', d_str)}"
            lines = [
                "BEGIN:VEVENT",
                f"UID:{uid}",
                f"DTSTART;VALUE=DATE:{d_obj.strftime('%Y%m%d')}",
                f"DTEND;VALUE=DATE:{next_d.strftime('%Y%m%d')}",
                f"SUMMARY:{ics_escape('🍽️ ' + title)}",
            ]
            if img_loc:
                lines.append(f"LOCATION:{ics_escape(img_loc)}")
            if desc_text:
                lines.append(f"DESCRIPTION:{ics_escape(desc_text)}")
            lines.append("END:VEVENT")
            vevents.append("\r\n".join(lines))
    elif feed_name == "family":
        if family_id:
            for ev in fetch_gcal_events(family_id):
                v = gcal_item_to_vevent(ev)
                if v:
                    vevents.append(v)
    elif feed_name == "holidays":
        if holiday_id:
            for ev in fetch_gcal_events(holiday_id):
                v = gcal_item_to_vevent(ev)
                if v:
                    vevents.append(v)
    elif feed_name.startswith("member_"):
        member = feed_name[len("member_"):]
        cals_to_poll = []
        if member in extra_member_cals:
            cals_to_poll.append(extra_member_cals[member])
        elif member in cal_ids:
            cals_to_poll.append(cal_ids[member])

        seen_uids = set()
        for cid in cals_to_poll:
            for ev in fetch_gcal_events(cid):
                uid = ev.get("id")
                if uid and uid not in seen_uids:
                    seen_uids.add(uid)
                    v = gcal_item_to_vevent(ev)
                    if v:
                        vevents.append(v)

        if family_id:
            for ev in fetch_gcal_events(family_id):
                uid = ev.get("id")
                if uid and uid in seen_uids:
                    continue
                owners = classify_family_event(ev)
                if member in owners:
                    seen_uids.add(uid)
                    v = gcal_item_to_vevent(ev)
                    if v:
                        vevents.append(v)

    body = "\r\n".join(vevents)
    header = (
        "BEGIN:VCALENDAR\r\n"
        "VERSION:2.0\r\n"
        "PRODID:-//HEARTH//Calendar Bridge//EN\r\n"
        "CALSCALE:GREGORIAN\r\n"
        "METHOD:PUBLISH\r\n"
        f"X-WR-CALNAME:HEARTH {feed_name}\r\n"
        "X-WR-TIMEZONE:UTC\r\n"
    )
    footer = "\r\nEND:VCALENDAR\r\n"
    return (header + body + footer).encode("utf-8")


PHOTOS_CACHE_FILE = "/app/data/photos_cache.json" if os.path.exists("/app/data") else "photos_cache.json"
PHOTOS_CACHE_LOCK = threading.Lock()
_PHOTO_URL_RE = re.compile(r"https://lh3\.googleusercontent\.com/pw/[a-zA-Z0-9_-]+")


def _load_photos_cache_unlocked():
    default_url = str(_cfg("GOOGLE_PHOTOS_ALBUM_URL", ""))
    default_auth_url = str(_cfg("GOOGLE_PHOTOS_AUTH_URL", default_url))
    default_title = str(_cfg("GOOGLE_PHOTOS_ALBUM_TITLE", "Shared Photos"))
    try:
        if os.path.exists(PHOTOS_CACHE_FILE):
            with open(PHOTOS_CACHE_FILE, "r", encoding="utf-8") as f:
                data = json.load(f)
                if isinstance(data, dict):
                    data.setdefault("album_title", default_title)
                    data.setdefault("album_url", default_url)
                    data.setdefault("album_auth_url", default_auth_url)
                    data.setdefault("photos", [])
                    return data
    except Exception:
        pass
    return {
        "album_title": default_title,
        "album_url": default_url,
        "album_auth_url": default_auth_url,
        "photos": [],
        "updated_at": None,
        "source": None,
    }


def _load_photos_cache():
    with PHOTOS_CACHE_LOCK:
        return _load_photos_cache_unlocked()


def _save_photos_cache(photos_list, source="ember-android", album_title="Shared Photos"):
    cleaned = []
    seen = set()
    for raw in photos_list or []:
        if not isinstance(raw, str):
            continue
        m = _PHOTO_URL_RE.search(raw)
        base = m.group(0) if m else (raw.split("=")[0] if raw.startswith("https://lh3.googleusercontent.com/") else "")
        if base and base not in seen:
            seen.add(base)
            cleaned.append(base)
    with PHOTOS_CACHE_LOCK:
        state = _load_photos_cache_unlocked()
        if cleaned:
            state["photos"] = cleaned
            state["updated_at"] = int(time.time())
            state["source"] = source
            state["album_title"] = album_title
            try:
                with open(PHOTOS_CACHE_FILE, "w", encoding="utf-8") as f:
                    json.dump(state, f, indent=2)
            except Exception:
                pass
        return state


def _try_direct_scrape_album(url):
    if not url or not _is_valid_album_url(url):
        return None
    try:
        req = urllib.request.Request(
            url,
            headers={
                "User-Agent": "Mozilla/5.0 (Linux; Android 14; Kiosk) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
            },
        )
        with urllib.request.urlopen(req, timeout=8) as resp:
            html = resp.read().decode("utf-8", errors="ignore")
        matches = _PHOTO_URL_RE.findall(html)
        if matches:
            return _save_photos_cache(matches, source="direct-share-link")
    except Exception:
        pass
    return None


def _derive_screens_from_config():
    cfg = _load_config()
    for k in ("screens", "screen_layouts"):
        if k in cfg and isinstance(cfg[k], list):
            return cfg[k]
    if "display" in cfg and isinstance(cfg["display"], dict):
        for k in ("screens", "screen_layouts"):
            if k in cfg["display"] and isinstance(cfg["display"][k], list):
                return cfg["display"][k]
    if "hearth" in cfg and isinstance(cfg["hearth"], dict):
        for k in ("screens", "screen_layouts"):
            if k in cfg["hearth"] and isinstance(cfg["hearth"][k], list):
                return cfg["hearth"][k]

    core_url = str(_cfg("MIRRORMERE_CORE_URL", _cfg("CORE_URL", "http://127.0.0.1:8080"))).rstrip("/")
    try:
        req = urllib.request.Request(f"{core_url}/api/screens")
        with urllib.request.urlopen(req, timeout=2) as r:
            if r.status == 200:
                data = json.loads(r.read().decode())
                if isinstance(data, dict) and "screens" in data:
                    return data["screens"]
                elif isinstance(data, list):
                    return data
    except Exception:
        pass

    widgets = []
    if "display" in cfg and isinstance(cfg["display"], dict):
        widgets = cfg["display"].get("widgets", [])
    if not widgets and "widgets" in cfg:
        widgets = cfg.get("widgets", [])

    if widgets and isinstance(widgets, list):
        screens = []
        current_screen = []
        current_cells = 0
        screen_idx = 0
        for w in widgets:
            if not isinstance(w, dict):
                continue
            wid = w.get("id") or w.get("widget_id")
            if not wid:
                continue
            dims = w.get("dimensions", [2, 1])
            cells = (dims[0] * dims[1]) if (isinstance(dims, list) and len(dims) >= 2) else 2
            if current_cells + cells > 12 and current_screen:
                screens.append({"screen_index": screen_idx, "widgets": current_screen})
                screen_idx += 1
                current_screen = []
                current_cells = 0
            current_screen.append({
                "widget_id": wid,
                "origin": w.get("origin", [current_cells % 6, (current_cells // 6) % 2]),
                "dimensions": dims,
            })
            current_cells += cells
        if current_screen:
            screens.append({"screen_index": screen_idx, "widgets": current_screen})
        if screens:
            return screens

    return [
        {"screen_index": 0, "widgets": [
            {"widget_id": "family-calendar", "origin": [0, 0], "dimensions": [4, 2]},
            {"widget_id": "home-weather", "origin": [4, 0], "dimensions": [2, 1]},
            {"widget_id": "household-tasks", "origin": [4, 1], "dimensions": [2, 1]},
        ]}
    ]


class Handler(BaseHTTPRequestHandler):
    def _is_internal_caller(self):
        client_ip = self.client_address[0] if self.client_address else ""
        return _is_internal_caller(client_ip)

    def _send_json_error(self, status, message):
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        payload = json.dumps({"error": message}).encode("utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path
        qs = urllib.parse.parse_qs(parsed.query)

        if path == "/healthz":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return

        if path.startswith("/ical/") and path.endswith(".ics"):
            if not self._is_internal_caller():
                self.send_response(403)
                self.end_headers()
                return
            feed = path[len("/ical/"):-4]
            payload = build_feed(feed)
            self.send_response(200)
            self.send_header("Content-Type", "text/calendar; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return

        if path.startswith("/hearth-api/"):
            if not self._is_internal_caller():
                self._send_json_error(403, "Forbidden: internal callers only")
                return

        if path.startswith("/hearth-api/mealie/image/"):
            recipe_id = path[len("/hearth-api/mealie/image/"):].strip("/")
            data, ctype = fetch_mealie_image(recipe_id)
            if data:
                self.send_response(200)
                self.send_header("Content-Type", ctype or "image/webp")
                self.send_header("Cache-Control", "public, max-age=86400")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                return
            self.send_response(404)
            self.end_headers()
            return

        if path == "/hearth-api/photos/state":
            state = _load_photos_cache()
            if not state.get("photos"):
                album_url = state.get("album_url", "")
                if album_url and _is_valid_album_url(album_url):
                    scraped = _try_direct_scrape_album(album_url)
                    if scraped:
                        state = scraped
            payload = json.dumps(state).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/plex/recent":
            items = fetch_plex_recent()
            payload = json.dumps({"items": items}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return

        if path == "/hearth-api/plex/upcoming":
            items = fetch_arr_upcoming()
            payload = json.dumps({"items": items}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return

        if path == "/hearth-api/screens/layout":
            screens = _derive_screens_from_config()
            payload = json.dumps({"screens": screens}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/music/state":
            payload = json.dumps(get_music_dashboard_state()).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/music/search":
            q = (qs.get("q") or [""])[0]
            payload = json.dumps({"items": search_yt_music(q)}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path.startswith("/hearth-api/music/stream/"):
            vid = path[len("/hearth-api/music/stream/"):].split(".")[0]
            if not vid or not YT_VIDEO_ID_RE.match(vid):
                self._send_json_error(400, "Invalid video_id")
                return
            stream_url, ctype = resolve_yt_audio_stream(vid)
            if not stream_url:
                self.send_response(404)
                self.end_headers()
                return
            headers = {"User-Agent": "Mozilla/5.0"}
            try:
                req = urllib.request.Request(stream_url, headers=headers)
                with urllib.request.urlopen(req, timeout=15) as upstream:
                    self.send_response(upstream.getcode() or 200)
                    self.send_header("Content-Type", ctype or "audio/mp4")
                    cl = upstream.headers.get("Content-Length")
                    if cl:
                        self.send_header("Content-Length", cl)
                    self.end_headers()
                    while True:
                        chunk = upstream.read(65536)
                        if not chunk:
                            break
                        self.wfile.write(chunk)
            except Exception as e:
                print(f"[HEARTH-Bridge] Stream proxy error: {e}", flush=True)
            return

        if path == "/hearth-api/plex/thumb":
            thumb_path = (qs.get("path") or [""])[0]
            data, ctype = fetch_plex_thumb(thumb_path)
            if data:
                self.send_response(200)
                self.send_header("Content-Type", ctype or "image/jpeg")
                self.send_header("Cache-Control", "public, max-age=604800")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                return
            self.send_response(404)
            self.end_headers()
            return

        if path == "/hearth-api/seerr/search":
            q = (qs.get("q") or [""])[0]
            items = seerr_search(q)
            payload = json.dumps({"items": items, "results": items}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path

        if path.startswith("/hearth-api/"):
            if not self._is_internal_caller():
                self._send_json_error(403, "Forbidden: internal callers only")
                return

        # Parse and validate Content-Length
        try:
            cl_header = self.headers.get("Content-Length")
            length = int(cl_header) if cl_header is not None else 0
            if length < 0:
                raise ValueError("Negative Content-Length")
        except (ValueError, TypeError) as e:
            self._send_json_error(400, f"Invalid Content-Length: {e}")
            return

        max_len = 262144 if path == "/hearth-api/photos/sync" else 8192
        if length > max_len:
            self._send_json_error(413, "Payload too large")
            return

        # Parse and validate JSON body
        try:
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            if not isinstance(body, dict):
                raise TypeError("Request body must be a JSON object")
        except (ValueError, TypeError, json.JSONDecodeError) as e:
            self._send_json_error(400, f"Malformed JSON: {e}")
            return

        if path == "/hearth-api/photos/sync":
            album_url = body.get("album_url")
            if album_url:
                if not _is_valid_album_url(str(album_url)):
                    self._send_json_error(400, "Invalid album_url; must be https://photos.app.goo.gl or https://photos.google.com")
                    return
            try:
                photos_list = body.get("photos") or []
                if not isinstance(photos_list, list):
                    raise TypeError("photos must be a list")
                source_label = str(body.get("source") or "ember-android")
                album_title = str(body.get("album_title") or _cfg("GOOGLE_PHOTOS_ALBUM_TITLE", "Shared Photos"))
                state = _save_photos_cache(
                    photos_list,
                    source=source_label,
                    album_title=album_title,
                )
                if album_url:
                    with PHOTOS_CACHE_LOCK:
                        state["album_url"] = str(album_url)
                        try:
                            with open(PHOTOS_CACHE_FILE, "w", encoding="utf-8") as f:
                                json.dump(state, f, indent=2)
                        except Exception:
                            pass
            except (ValueError, TypeError) as e:
                self._send_json_error(400, f"Invalid sync parameters: {e}")
                return

            payload = json.dumps({
                "ok": True,
                "count": len(state.get("photos", [])),
                "photos": state.get("photos", []),
            }).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/plex/play":
            ok, res = launch_plex_on_living_room_tv(body)
            payload = json.dumps({"ok": ok, **res}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/music/play":
            ok, st = start_music_playback(body)
            payload = json.dumps({"ok": ok, "state": st}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/music/control":
            action = body.get("action", "")
            try:
                if action == "volume" and "value" in body:
                    float(body["value"])
                if action == "play_index" and "index" in body:
                    int(body["index"])
            except (ValueError, TypeError) as e:
                self._send_json_error(400, f"Invalid numeric argument: {e}")
                return
            ok, st = control_music_playback(body)
            payload = json.dumps({"ok": ok, "state": st}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/hearth-api/seerr/request":
            media_type = body.get("media_type")
            tmdb_id_raw = body.get("tmdb_id")
            if tmdb_id_raw is None:
                self._send_json_error(400, "Missing tmdb_id")
                return
            try:
                tmdb_id = int(tmdb_id_raw)
            except (ValueError, TypeError) as e:
                self._send_json_error(400, f"Invalid tmdb_id: {e}")
                return
            ok, res = seerr_request(media_type, tmdb_id)
            payload = json.dumps({"ok": ok, "result": res}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    _orig_getaddrinfo = socket.getaddrinfo

    def _ipv4_getaddrinfo(host, port, family=0, type=0, proto=0, flags=0):
        if family == 0 or family == socket.AF_UNSPEC:
            family = socket.AF_INET
        return _orig_getaddrinfo(host, port, family, type, proto, flags)

    socket.getaddrinfo = _ipv4_getaddrinfo

    threading.Thread(target=_music_auto_advance_loop, daemon=True).start()
    srv = ThreadingHTTPServer(("0.0.0.0", 8099), Handler)
    print("[HEARTH-Bridge] Listening on :8099", flush=True)
    srv.serve_forever()
