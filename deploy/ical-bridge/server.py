import socket
# AF_INET_PATCHED: Force IPv4 resolution inside Docker bridge network
_orig_getaddrinfo = socket.getaddrinfo
def _ipv4_getaddrinfo(host, port, family=0, type=0, proto=0, flags=0):
    if family == 0 or family == socket.AF_UNSPEC:
        family = socket.AF_INET
    return _orig_getaddrinfo(host, port, family, type, proto, flags)
socket.getaddrinfo = _ipv4_getaddrinfo

#!/usr/bin/env python3
"""HEARTH Configurable OAuth-to-iCal, Mealie Recipe + Image Proxy & Plex/Arr/Seerr Bridge."""

from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor
import datetime
import ipaddress
import json
import os
import sys
import re
import threading
import time
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from zoneinfo import ZoneInfo

PT = ZoneInfo(os.environ.get("TZ", "America/Los_Angeles"))
UTC = datetime.timezone.utc

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


def _load_config():
    for path in (
        os.environ.get("HEARTH_CONFIG_FILE", ""),
        "/secrets/hearth_env.json",
        "/secrets/env.json",
    ):
        if path and os.path.exists(path):
            try:
                with open(path) as f:
                    return json.load(f)
            except Exception:
                pass
    return {}


def _cfg(key, default=""):
    env_val = os.environ.get(key)
    if env_val is not None and env_val != "":
        return env_val
    return _load_config().get(key, default)


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


def _is_internal_caller(client_ip):
    try:
        addr = ipaddress.ip_address(client_ip)
        if addr.is_loopback:
            return True
        if addr.is_private:
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
        resp = json.loads(urllib.request.urlopen(req, timeout=10).read().decode())
        TOKEN_CACHE["token"] = resp["access_token"]
        TOKEN_CACHE["exp"] = now + int(resp.get("expires_in", 3500))
        return TOKEN_CACHE["token"]


def fetch_gcal_events(cal_id):
    if not cal_id:
        return []
    key = f"gcal:{cal_id}"
    now = time.time()
    if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
        return CACHE[key][1]
    with _get_lock(key):
        now = time.time()
        if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
            return CACHE[key][1]
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
        try:
            items = json.loads(urllib.request.urlopen(req, timeout=6).read().decode()).get("items", [])
        except Exception as e:
            print(f"[HEARTH-Bridge] Warning fetching {cal_id}: {e}", flush=True)
            items = CACHE.get(key, (0, []))[1]
        CACHE[key] = (now, items)
        return items


def fetch_mealie_recipe_detail(slug, base, tok):
    if not slug:
        return {}
    key = f"mealie_recipe:{slug}"
    now = time.time()
    if key in CACHE and now - CACHE[key][0] < IMG_CACHE_TTL:
        return CACHE[key][1]
    try:
        req = urllib.request.Request(
            f"{base}/api/recipes/{urllib.parse.quote(slug)}",
            headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"},
        )
        detail = json.loads(urllib.request.urlopen(req, timeout=5).read().decode())
        CACHE[key] = (now, detail)
        return detail
    except Exception as e:
        print(f"[HEARTH-Bridge] Recipe detail error for {slug}: {e}", flush=True)
        return CACHE.get(key, (0, {}))[1]


def fetch_mealie_events():
    key = "mealie"
    now = time.time()
    if key in CACHE and now - CACHE[key][0] < CACHE_TTL:
        return CACHE[key][1]
    with _get_lock(key):
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
            items = json.loads(urllib.request.urlopen(req, timeout=8).read().decode()).get("items", [])
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
            items = CACHE.get(key, (0, []))[1]
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
    if key in CACHE and now - CACHE[key][0] < 180:
        return CACHE[key][1]
    with _get_lock(key):
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
            data = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())
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
            out = CACHE.get(key, (0, []))[1]
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
    if key in CACHE and now - CACHE[key][0] < 300:
        return CACHE[key][1]
    with _get_lock(key):
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
                eps = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())
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
                movies = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())
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
        CACHE[key] = (now, items)
        return items


def seerr_search(query):
    if not query or not query.strip():
        return []
    base = str(_cfg("SEERR_BASE_URL", "")).rstrip("/")
    key = str(_cfg("SEERR_API_KEY", ""))
    if not base or not key:
        return []
    url = f"{base}/api/v1/search?query={urllib.parse.quote(query.strip())}&page=1&language=en"
    req = urllib.request.Request(url, headers={"X-Api-Key": key, "Accept": "application/json"})
    try:
        data = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())
        out = []
        for r in (data.get("results") or [])[:12]:
            mtype = r.get("mediaType")
            if mtype not in ("movie", "tv"):
                continue
            title = r.get("title") or r.get("name") or "Untitled"
            date_s = r.get("releaseDate") or r.get("firstAirDate") or ""
            year = date_s[:4] if date_s else ""
            poster_path = r.get("posterPath") or ""
            thumb = f"https://image.tmdb.org/t/p/w300{poster_path}" if poster_path else ""
            minfo = r.get("mediaInfo") or {}
            status_code = minfo.get("status", 1)
            status = "none"
            if status_code in (4, 5):
                status = "available"
            elif status_code in (2, 3):
                status = "requested"
            out.append({
                "tmdb_id": r.get("id"),
                "media_type": mtype,
                "title": title,
                "year": year,
                "thumb": thumb,
                "status": status,
                "summary": (r.get("overview") or "")[:200],
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
    body = {"mediaType": media_type, "mediaId": int(tmdb_id)}
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
    "active_speaker": "media_player.home_group",
    "queue": [],
    "queue_index": 0,
    "playlist_title": "",
    "shuffle": True,
    "last_cast_ts": 0,
    "playing_video_id": "",
}

SPEAKER_LIST = [
    {"entity_id": "media_player.home_group", "name": "Home Group"},
    {"entity_id": "media_player.whole_house", "name": "Whole House"},
    {"entity_id": "media_player.kitchen_speaker", "name": "Kitchen"},
    {"entity_id": "media_player.living_room_speaker", "name": "Living Room"},
    {"entity_id": "media_player.office_speaker", "name": "Office"},
    {"entity_id": "media_player.bedroom_speaker", "name": "Bedroom"},
    {"entity_id": "media_player.rosie_s_speaker", "name": "Rosie's Room"},
    {"entity_id": "media_player.isaac_s_speaker", "name": "Isaac's Room"},
    {"entity_id": "media_player.kitchen_hub", "name": "Living Room Hub"},
]


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
        yt_cfg = _cfg("YT_MUSIC_OAUTH", {}) or {}
        if not yt_cfg:
            return ""
        if yt_cfg.get("access_token") and now < float(yt_cfg.get("expires_at", 0)) - 60:
            YT_TOKEN_CACHE["token"] = yt_cfg["access_token"]
            YT_TOKEN_CACHE["exp"] = float(yt_cfg["expires_at"])
            return YT_TOKEN_CACHE["token"]
        client_id = yt_cfg.get("client_id", "")
        client_secret = yt_cfg.get("client_secret", "")
        refresh_token = yt_cfg.get("refresh_token", "")
        if not (client_id and client_secret and refresh_token):
            return yt_cfg.get("access_token", "")
        data = urllib.parse.urlencode({
            "client_id": client_id,
            "client_secret": client_secret,
            "refresh_token": refresh_token,
            "grant_type": "refresh_token",
        }).encode()
        try:
            req = urllib.request.Request("https://oauth2.googleapis.com/token", data=data)
            resp = json.loads(urllib.request.urlopen(req, timeout=10).read().decode())
            YT_TOKEN_CACHE["token"] = resp["access_token"]
            YT_TOKEN_CACHE["exp"] = now + int(resp.get("expires_in", 3500))
            return YT_TOKEN_CACHE["token"]
        except Exception as e:
            print(f"[HEARTH-Bridge] YT OAuth refresh error: {e}", flush=True)
            return yt_cfg.get("access_token", "")


def fetch_yt_playlists():
    key = "yt:playlists"
    now = time.time()
    if key in CACHE and now - CACHE[key][0] < 600:
        return CACHE[key][1]
    tok = get_yt_oauth_token()
    if not tok:
        return []
    url = "https://www.googleapis.com/youtube/v3/playlists?part=snippet,contentDetails&mine=true&maxResults=25"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"})
    out = []
    try:
        data = json.loads(urllib.request.urlopen(req, timeout=8).read().decode())
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
        CACHE[key] = (now, out)
    except Exception as e:
        print(f"[HEARTH-Bridge] YT playlists error: {e}", flush=True)
        out = CACHE.get(key, (0, []))[1]
    return out


def fetch_yt_playlist_tracks(playlist_id):
    tok = get_yt_oauth_token()
    if not tok or not playlist_id:
        return []
    url = (
        "https://www.googleapis.com/youtube/v3/playlistItems"
        f"?part=snippet&playlistId={urllib.parse.quote(playlist_id)}&maxResults=50"
    )
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}", "Accept": "application/json"})
    tracks = []
    try:
        data = json.loads(urllib.request.urlopen(req, timeout=10).read().decode())
        for item in data.get("items", []):
            snip = item.get("snippet") or {}
            res_id = snip.get("resourceId") or {}
            vid = res_id.get("videoId")
            title = snip.get("title") or ""
            if not vid or title in ("Deleted video", "Private video"):
                continue
            thumbs = snip.get("thumbnails") or {}
            thumb = (
                (thumbs.get("high") or {}).get("url")
                or (thumbs.get("medium") or {}).get("url")
                or (thumbs.get("default") or {}).get("url")
                or f"https://i.ytimg.com/vi/{vid}/hqdefault.jpg"
            )
            artist = (snip.get("videoOwnerChannelTitle") or "YouTube Music").replace(" - Topic", "")
            tracks.append({
                "video_id": vid,
                "title": title,
                "artist": artist,
                "thumb": thumb,
            })
    except Exception as e:
        print(f"[HEARTH-Bridge] YT playlistItems error: {e}", flush=True)
    return tracks


def search_yt_music(query):
    if not query or not query.strip():
        return []
    try:
        import ytmusicapi
        ytm = ytmusicapi.YTMusic()
        results = ytm.search(query.strip(), filter="songs", limit=15)
        out = []
        for r in results:
            vid = r.get("videoId")
            if not vid:
                continue
            artists = ", ".join(a.get("name", "") for a in (r.get("artists") or []) if a.get("name"))
            thumbs = r.get("thumbnails") or []
            thumb = thumbs[-1].get("url", "") if thumbs else f"https://i.ytimg.com/vi/{vid}/hqdefault.jpg"
            out.append({
                "video_id": vid,
                "title": r.get("title") or "Untitled",
                "artist": artists or "YouTube Music",
                "album": (r.get("album") or {}).get("name") or "",
                "duration": r.get("duration") or "",
                "thumb": thumb,
            })
        return out
    except Exception as e:
        print(f"[HEARTH-Bridge] search_yt_music error: {e}", flush=True)
        return []


def resolve_yt_audio_stream(video_id):
    if not video_id or not re.match(r"^[0-9a-zA-Z_-]{6,20}$", str(video_id)):
        return None, None
    now = time.time()
    with STREAM_URL_LOCK:
        cached = STREAM_URL_CACHE.get(video_id)
        if cached and now - cached[0] < 1200:
            return cached[1], cached[2]
    try:
        import yt_dlp
        ydl_opts = {
            "format": "bestaudio[ext=m4a]/bestaudio",
            "quiet": True,
            "no_warnings": True,
            "noplaylist": True,
        }
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            info = ydl.extract_info(f"https://music.youtube.com/watch?v={video_id}", download=False)
            stream_url = info.get("url")
            ext = info.get("ext", "m4a")
            ctype = "audio/mp4" if ext == "m4a" else (info.get("acodec") and "audio/webm" or "audio/mp4")
            if stream_url:
                with STREAM_URL_LOCK:
                    STREAM_URL_CACHE[video_id] = (now, stream_url, ctype)
                return stream_url, ctype
    except Exception as e:
        print(f"[HEARTH-Bridge] yt_dlp extraction error for {video_id}: {e}", flush=True)
    return None, None


def _cast_track_to_speaker(speaker_entity, track):
    vid = track.get("video_id")
    if not vid or not speaker_entity:
        return False
    # Pre-warm the stream URL before instructing Google Cast speaker to fetch it
    stream_url, _ = resolve_yt_audio_stream(vid)
    if not stream_url:
        return False
    bridge_host = str(_cfg("BRIDGE_LAN_URL", "http://127.0.0.1:8099")).rstrip("/")
    proxy_stream_url = f"{bridge_host}/hearth-api/music/stream/{vid}.m4a"
    payload = {
        "entity_id": speaker_entity,
        "media_content_id": proxy_stream_url,
        "media_content_type": "audio/mp4",
        "extra": {
            "title": track.get("title", "YouTube Music"),
            "thumb": track.get("thumb", ""),
            "metadata": {
                "metadataType": 3,
                "title": track.get("title", "YouTube Music"),
                "artist": track.get("artist", ""),
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
    with MUSIC_STATE_LOCK:
        active_spk = MUSIC_STATE["active_speaker"]
        queue = list(MUSIC_STATE["queue"])
        q_idx = MUSIC_STATE["queue_index"]
        pl_title = MUSIC_STATE["playlist_title"]
        shuffle = MUSIC_STATE["shuffle"]

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
        "speakers": SPEAKER_LIST,
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
    elif action == "play_pause":
        _ha_request("POST", "/api/services/media_player/media_play_pause", {"entity_id": active_spk})
    elif action == "stop":
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["playing_video_id"] = ""
        _ha_request("POST", "/api/services/media_player/media_stop", {"entity_id": active_spk})
    elif action == "volume":
        vol = max(0.0, min(1.0, float(body.get("value", 35)) / 100.0))
        _ha_request("POST", "/api/services/media_player/volume_set", {"entity_id": active_spk, "volume_level": vol})
    elif action == "next" and queue:
        next_idx = (q_idx + 1) % len(queue)
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["queue_index"] = next_idx
        _cast_track_to_speaker(active_spk, queue[next_idx])
    elif action == "prev" and queue:
        prev_idx = (q_idx - 1) % len(queue)
        with MUSIC_STATE_LOCK:
            MUSIC_STATE["queue_index"] = prev_idx
        _cast_track_to_speaker(active_spk, queue[prev_idx])
    elif action == "play_index" and queue:
        idx = int(body.get("index", 0))
        if 0 <= idx < len(queue):
            with MUSIC_STATE_LOCK:
                MUSIC_STATE["queue_index"] = idx
            _cast_track_to_speaker(active_spk, queue[idx])

    time.sleep(0.35)
    return True, get_music_dashboard_state()


def _music_auto_advance_loop():
    while True:
        time.sleep(5)
        try:
            with MUSIC_STATE_LOCK:
                vid = MUSIC_STATE["playing_video_id"]
                last_ts = MUSIC_STATE["last_cast_ts"]
                active_spk = MUSIC_STATE["active_speaker"]
                queue = list(MUSIC_STATE["queue"])
                q_idx = MUSIC_STATE["queue_index"]
            if not vid or len(queue) <= 1 or time.time() - last_ts < 15:
                continue
            st = _ha_request("GET", f"/api/states/{active_spk}", timeout=4) or {}
            if st.get("state") == "idle":
                next_idx = (q_idx + 1) % len(queue)
                with MUSIC_STATE_LOCK:
                    MUSIC_STATE["queue_index"] = next_idx
                _cast_track_to_speaker(active_spk, queue[next_idx])
        except Exception:
            pass

threading.Thread(target=_music_auto_advance_loop, daemon=True).start()


def launch_plex_on_living_room_tv(body):
    rating_key = str(body.get("rating_key") or body.get("id") or "").strip()
    mtype = str(body.get("type") or "movie").lower()
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
        root_data = json.loads(urllib.request.urlopen(req_root, timeout=6).read().decode())
        machine_id = str(root_data["MediaContainer"]["machineIdentifier"])

        target_rk = rating_key
        # If a season or show was tapped, resolve the first unwatched episode (or first episode)
        if mtype in ("season", "show"):
            leaves_url = f"{plex_base}/library/metadata/{urllib.parse.quote(rating_key)}/allLeaves"
            req_leaves = urllib.request.Request(
                leaves_url,
                headers={"X-Plex-Token": plex_token, "Accept": "application/json"},
            )
            leaves_data = json.loads(urllib.request.urlopen(req_leaves, timeout=6).read().decode())
            leaves = leaves_data.get("MediaContainer", {}).get("Metadata", [])
            if leaves:
                unwatched = [ep for ep in leaves if not ep.get("viewCount")]
                chosen = unwatched[0] if unwatched else leaves[0]
                target_rk = str(chosen.get("ratingKey") or rating_key)

        meta_key = urllib.parse.quote(f"/library/metadata/{target_rk}", safe="")
        deep_link = f"plex://play/?metadataKey={meta_key}&server={machine_id}"

        def _bg_wake_and_play():
            rem_st = _ha_request("GET", "/api/states/remote.living_room_tv") or {}
            mp_st = _ha_request("GET", "/api/states/media_player.living_room_tv") or {}
            is_cold = (
                rem_st.get("state") != "on"
                or mp_st.get("state") in ("off", "standby", "unavailable", None)
            )
            if is_cold:
                _ha_request("POST", "/api/services/remote/turn_on", {"entity_id": "remote.living_room_tv"})
                time.sleep(9.0)

            cast_st = _ha_request("GET", "/api/states/media_player.living_room_living_room_tv") or {}
            if (
                cast_st.get("state") in ("playing", "paused", "idle")
                and (cast_st.get("attributes") or {}).get("app_id") not in (None, "AndroidNativeApp")
            ):
                _ha_request("POST", "/api/services/media_player/turn_off", {"entity_id": "media_player.living_room_living_room_tv"})
                time.sleep(1.0)

            _ha_request(
                "POST",
                "/api/services/media_player/play_media",
                {
                    "entity_id": "media_player.living_room_tv",
                    "media_content_type": "url",
                    "media_content_id": deep_link,
                },
            )
            time.sleep(3.5)
            _ha_request(
                "POST",
                "/api/services/remote/send_command",
                {"entity_id": "remote.living_room_tv", "command": "DPAD_CENTER"},
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
    prep = detail.get("prepTime") or recipe.get("prepTime")
    cook = detail.get("performTime") or detail.get("cookTime") or recipe.get("performTime")
    total = detail.get("totalTime") or recipe.get("totalTime")
    times = []
    if prep:
        times.append(f"Prep: {prep}")
    if cook:
        times.append(f"Cook: {cook}")
    if total:
        times.append(f"Total: {total}")
    if times:
        parts.append("⏱️ " + " · ".join(times))

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
                if not classify_family_event(ev):
                    v = gcal_item_to_vevent(ev)
                    if v:
                        vevents.append(v)
        if holiday_id:
            for ev in fetch_gcal_events(holiday_id):
                v = gcal_item_to_vevent(ev)
                if v:
                    vevents.append(v)
    else:
        direct_id = cal_ids.get(feed_name, "")
        if direct_id:
            for ev in fetch_gcal_events(direct_id):
                v = gcal_item_to_vevent(ev)
                if v:
                    vevents.append(v)
        if family_id:
            for ev in fetch_gcal_events(family_id):
                if feed_name in classify_family_event(ev):
                    v = gcal_item_to_vevent(ev)
                    if v:
                        vevents.append(v)
        for extra_key in (extra_member_cals.get(feed_name) or []):
            extra_id = cal_ids.get(extra_key, extra_key)
            for ev in fetch_gcal_events(extra_id):
                v = gcal_item_to_vevent(ev)
                if v:
                    vevents.append(v)

    body = "\r\n".join([
        "BEGIN:VCALENDAR",
        "VERSION:2.0",
        f"PRODID:-//HEARTH//{feed_name.upper()}//EN",
        "CALSCALE:GREGORIAN",
        *vevents,
        "END:VCALENDAR",
        "",
    ])
    return body.encode("utf-8")



PHOTOS_CACHE_FILE = os.environ.get("PHOTOS_CACHE_FILE", "/secrets/photos_cache.json")
_PHOTO_URL_RE = re.compile(r"https://lh3\.googleusercontent\.com/pw/[A-Za-z0-9_-]{20,}")


def _load_photos_cache():
    default_url = str(_cfg("GOOGLE_PHOTOS_ALBUM_URL", ""))
    default_auth_url = str(_cfg("GOOGLE_PHOTOS_AUTH_URL", default_url))
    default_title = str(_cfg("GOOGLE_PHOTOS_ALBUM_TITLE", "Rosie and Isaac"))
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


def _save_photos_cache(photos_list, source="ember-android", album_title="Rosie and Isaac"):
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
    state = _load_photos_cache()
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
    if not url:
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


class Handler(BaseHTTPRequestHandler):
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
            if not _is_internal_caller(self.client_address[0]):
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
                scraped = _try_direct_scrape_album(state.get("album_url", ""))
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
            screens = [
                {"screen_index": 0, "widgets": [
                    {"widget_id": "hearth-family-grid", "origin": [0, 0], "dimensions": [2, 4]},
                    {"widget_id": "whole-foods-tasks", "origin": [0, 4], "dimensions": [1, 2]},
                    {"widget_id": "family-photos", "origin": [1, 4], "dimensions": [1, 2]},
                ]},
                {"screen_index": 1, "widgets": [
                    {"widget_id": "youtube-music-card", "origin": [0, 0], "dimensions": [2, 3]},
                    {"widget_id": "new-on-plex", "origin": [0, 3], "dimensions": [2, 3]},
                ]},
                {"screen_index": 2, "widgets": [
                    {"widget_id": "home-assistant-dashboard", "origin": [0, 0], "dimensions": [2, 6]},
                ]},
            ]
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
            stream_url, ctype = resolve_yt_audio_stream(vid)
            if not stream_url:
                self.send_response(404)
                self.end_headers()
                return
            headers = {"User-Agent": "Mozilla/5.0"}
            if "Range" in self.headers:
                headers["Range"] = self.headers["Range"]
            try:
                req = urllib.request.Request(stream_url, headers=headers)
                with urllib.request.urlopen(req, timeout=15) as upstream:
                    self.send_response(upstream.getcode() or 200)
                    self.send_header("Content-Type", ctype or "audio/mp4")
                    for h in ("Content-Length", "Content-Range", "Accept-Ranges"):
                        if upstream.headers.get(h):
                            self.send_header(h, upstream.headers.get(h))
                    self.end_headers()
                    while True:
                        chunk = upstream.read(65536)
                        if not chunk:
                            break
                        self.wfile.write(chunk)
            except (BrokenPipeError, ConnectionResetError):
                pass
            except Exception as e:
                print(f"[HEARTH-Bridge] Stream proxy error: {e}", flush=True)
            return

        if path == "/hearth-api/seerr/search":
            q = (qs.get("q") or [""])[0]
            items = seerr_search(q)
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

        if path == "/hearth-api/plex/thumb":
            thumb_path = (qs.get("path") or [""])[0]
            data, ctype = fetch_plex_thumb(thumb_path)
            if data:
                self.send_response(200)
                self.send_header("Content-Type", ctype or "image/jpeg")
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

        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/hearth-api/photos/sync":
            length = min(int(self.headers.get("Content-Length") or 0), 262144)
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            state = _save_photos_cache(
                body.get("photos") or [],
                source=str(body.get("source") or "ember-android"),
                album_title=str(body.get("album_title") or "Rosie and Isaac"),
            )
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

        if parsed.path == "/hearth-api/plex/play":
            length = min(int(self.headers.get("Content-Length") or 0), 8192)
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            ok, res = launch_plex_on_living_room_tv(body)
            payload = json.dumps({"ok": ok, **res}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if parsed.path == "/hearth-api/music/play":
            length = min(int(self.headers.get("Content-Length") or 0), 8192)
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            ok, st = start_music_playback(body)
            payload = json.dumps({"ok": ok, "state": st}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if parsed.path == "/hearth-api/music/control":
            length = min(int(self.headers.get("Content-Length") or 0), 8192)
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            ok, st = control_music_playback(body)
            payload = json.dumps({"ok": ok, "state": st}).encode("utf-8")
            self.send_response(200 if ok else 500)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if parsed.path == "/hearth-api/seerr/request":
            length = min(int(self.headers.get("Content-Length") or 0), 8192)
            raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
            body = json.loads(raw)
            ok, res = seerr_request(body.get("media_type"), body.get("tmdb_id"))
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
    srv = ThreadingHTTPServer(("0.0.0.0", 8099), Handler)
    print("[HEARTH-Bridge] Listening on :8099", flush=True)
    srv.serve_forever()
