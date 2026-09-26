"""
test_fetcher.py - Hermetic unit tests for ETagFetcher
Reference: SPEC-009 §1-§3, Issue #246
"""

import io
import unittest
import urllib.error

from fetcher import ETagFetcher, FetchResult


class MockHTTPResponse:
    def __init__(self, status: int, data: bytes = b"", headers: dict | None = None):
        self.status = status
        self.code = status
        self.data = data
        self.headers = headers or {}

    def read(self) -> bytes:
        return self.data

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        pass


class TestETagFetcher(unittest.TestCase):
    def test_fetch_200_ok(self):
        """
        Validates standard 200 OK response with 1-bit image bytes and ETag.
        """
        captured_requests = []

        def mock_opener(req, timeout=None):
            captured_requests.append(req)
            return MockHTTPResponse(
                status=200,
                data=b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR...",
                headers={"ETag": '"etag-abc-123"'},
            )

        fetcher = ETagFetcher(image_url="http://localhost:8081/eink.png", timeout=5.0, opener=mock_opener)
        res = fetcher.fetch()

        self.assertEqual(res.status_code, 200)
        self.assertEqual(res.etag, '"etag-abc-123"')
        self.assertFalse(res.not_modified)
        self.assertTrue(res.data.startswith(b"\x89PNG"))
        self.assertIsNone(res.error)

        self.assertEqual(len(captured_requests), 1)
        self.assertNotIn("If-none-match", captured_requests[0].headers)

    def test_fetch_304_not_modified(self):
        """
        Validates 304 Not Modified response when If-None-Match matches sidecar cache.
        """
        captured_requests = []

        def mock_opener(req, timeout=None):
            captured_requests.append(req)
            # Python urllib raises HTTPError for 304 responses
            hdrs = {"ETag": '"cached-etag-456"'}
            raise urllib.error.HTTPError(
                url=req.full_url,
                code=304,
                msg="Not Modified",
                hdrs=hdrs,
                fp=io.BytesIO(b""),
            )

        fetcher = ETagFetcher(image_url="http://localhost:8081/eink.png", timeout=5.0, opener=mock_opener)
        res = fetcher.fetch(last_etag='"cached-etag-456"')

        self.assertEqual(res.status_code, 304)
        self.assertEqual(res.etag, '"cached-etag-456"')
        self.assertTrue(res.not_modified)
        self.assertIsNone(res.data)
        self.assertIsNone(res.error)

        self.assertEqual(len(captured_requests), 1)
        self.assertEqual(captured_requests[0].headers.get("If-none-match"), '"cached-etag-456"')

    def test_fetch_http_503_degraded(self):
        """
        Validates graceful handling of upstream HTTP 503 error without crashing.
        """
        def mock_opener(req, timeout=None):
            raise urllib.error.HTTPError(
                url=req.full_url,
                code=503,
                msg="Service Unavailable",
                hdrs={},
                fp=io.BytesIO(b'{"error":"render failed"}'),
            )

        fetcher = ETagFetcher(image_url="http://localhost:8081/eink.png", opener=mock_opener)
        res = fetcher.fetch()

        self.assertEqual(res.status_code, 503)
        self.assertFalse(res.not_modified)
        self.assertIsNone(res.data)
        self.assertIn("503", res.error)

    def test_fetch_network_timeout(self):
        """
        Validates handling of socket timeout or connection failure.
        """
        def mock_opener(req, timeout=None):
            raise TimeoutError("connection timed out")

        fetcher = ETagFetcher(image_url="http://localhost:8081/eink.png", opener=mock_opener)
        res = fetcher.fetch()

        self.assertEqual(res.status_code, 0)
        self.assertFalse(res.not_modified)
        self.assertIn("timed out", res.error)


if __name__ == "__main__":
    unittest.main()
