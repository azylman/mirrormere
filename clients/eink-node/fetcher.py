"""
fetcher.py - ETag-based HTTP image fetcher for Mirrormere E-Ink display node
Reference: SPEC-009 §1-§3, SPEC-003 §4-§5
"""

from dataclasses import dataclass
import urllib.request
import urllib.error
import urllib.parse


@dataclass
class FetchResult:
    status_code: int
    etag: str | None
    data: bytes | None
    not_modified: bool
    error: str | None = None


class ETagFetcher:
    """
    Queries GET /eink.png with If-None-Match: <last_etag>.
    Skips downloading or processing on 304 Not Modified.
    """

    def __init__(self, image_url: str, timeout: float = 10.0, opener=None):
        self.image_url = image_url
        self.timeout = timeout
        self.opener = opener or urllib.request.urlopen

    def fetch(self, last_etag: str | None = None) -> FetchResult:
        headers = {
            "Accept": "image/png",
            "User-Agent": "Mirrormere-EInk-Node/1.0",
        }
        if last_etag:
            headers["If-None-Match"] = last_etag.strip()

        req = urllib.request.Request(self.image_url, headers=headers, method="GET")

        try:
            with self.opener(req, timeout=self.timeout) as resp:
                status = getattr(resp, "status", getattr(resp, "code", 200))
                raw_etag = resp.headers.get("ETag") if hasattr(resp, "headers") else None
                etag = raw_etag.strip() if raw_etag else None
                data = resp.read()
                return FetchResult(
                    status_code=status,
                    etag=etag,
                    data=data,
                    not_modified=False,
                )
        except urllib.error.HTTPError as e:
            if e.code == 304:
                raw_etag = e.headers.get("ETag") if hasattr(e, "headers") and e.headers else None
                etag = raw_etag.strip() if raw_etag else last_etag
                return FetchResult(
                    status_code=304,
                    etag=etag,
                    data=None,
                    not_modified=True,
                )
            return FetchResult(
                status_code=e.code,
                etag=None,
                data=None,
                not_modified=False,
                error=f"HTTP {e.code}: {e.reason}",
            )
        except Exception as e:
            return FetchResult(
                status_code=0,
                etag=None,
                data=None,
                not_modified=False,
                error=str(e),
            )
