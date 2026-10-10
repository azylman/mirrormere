#!/usr/bin/env python3
"""Unit tests for Mirrormere Nginx proxy sidecar configuration and templates.

Validates unprivileged execution requirements, WebSocket upgrade mapping,
route rules, header propagation, template variable substitution, and Dockerfile specs.
"""

import re
import unittest
from pathlib import Path

PROXY_DIR = Path(__file__).resolve().parent.parent
NGINX_CONF_PATH = PROXY_DIR / "nginx.conf"
TEMPLATE_PATH = PROXY_DIR / "templates" / "default.conf.template"
DOCKERFILE_PATH = PROXY_DIR / "Dockerfile"
README_PATH = PROXY_DIR / "README.md"


def extract_block(text: str, pattern: str) -> str:
    """Extract block body delimited by braces following a regex pattern match."""
    match = re.search(pattern, text)
    if not match:
        return ""
    brace_open = text.find("{", match.start())
    if brace_open == -1:
        return ""
    depth = 1
    i = brace_open + 1
    while i < len(text) and depth > 0:
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
        i += 1
    return text[brace_open + 1 : i - 1]


class TestProxyNginxConf(unittest.TestCase):
    """Validates root nginx.conf settings for unprivileged execution and WebSocket upgrade mapping."""

    def setUp(self):
        self.assertTrue(NGINX_CONF_PATH.exists(), f"Missing {NGINX_CONF_PATH}")
        self.content = NGINX_CONF_PATH.read_text(encoding="utf-8")

    def test_unprivileged_pid_and_temp_paths(self):
        """Verifies that nginx is configured with write paths in /tmp/ for unprivileged execution."""
        self.assertIn("pid /tmp/nginx.pid;", self.content)
        self.assertIn("client_body_temp_path /tmp/client_temp;", self.content)
        self.assertIn("proxy_temp_path /tmp/proxy_temp_path;", self.content)
        self.assertIn("fastcgi_temp_path /tmp/fastcgi_temp;", self.content)
        self.assertIn("uwsgi_temp_path /tmp/uwsgi_temp;", self.content)
        self.assertIn("scgi_temp_path /tmp/scgi_temp;", self.content)

    def test_websocket_upgrade_map(self):
        """Verifies map directive for dynamic Connection header switching based on $http_upgrade."""
        pattern = r"map\s+\$http_upgrade\s+\$connection_upgrade\s*\{\s*default\s+upgrade;\s*''\s+close;\s*\}"
        self.assertIsNotNone(
            re.search(pattern, self.content, re.MULTILINE),
            "Expected map $http_upgrade $connection_upgrade block in nginx.conf",
        )

    def test_client_max_body_size(self):
        """Verifies 20M body size ceiling to accommodate voice enrollment models and media."""
        self.assertIn("client_max_body_size 20M;", self.content)

    def test_conf_d_inclusion(self):
        """Verifies conf.d template target directory inclusion."""
        self.assertIn("include /etc/nginx/conf.d/*.conf;", self.content)


class TestProxyTemplateSubstitution(unittest.TestCase):
    """Validates environment variable substitution in default.conf.template."""

    def setUp(self):
        self.assertTrue(TEMPLATE_PATH.exists(), f"Missing {TEMPLATE_PATH}")
        self.template_raw = TEMPLATE_PATH.read_text(encoding="utf-8")

    def simulate_envsubst(self, template: str, env: dict) -> str:
        """Simulates envsubst by replacing only explicit ${VAR} or $VAR tokens in env."""
        rendered = template
        for key, val in env.items():
            rendered = rendered.replace(f"${{{key}}}", val)
            rendered = re.sub(rf"\${key}\b", val, rendered)
        return rendered

    def test_default_env_substitution(self):
        """Validates substitution using standard defaults."""
        defaults = {
            "PROXY_PORT": "8080",
            "CORE_HOST": "mirrormere-core:8080",
            "REMOTE_HOST": "mirrormere-remote:8092",
            "GO2RTC_HOST": "mirrormere-go2rtc:1984",
        }
        rendered = self.simulate_envsubst(self.template_raw, defaults)
        self.assertIn("listen 8080;", rendered)
        self.assertIn("proxy_pass http://mirrormere-core:8080;", rendered)
        self.assertIn("proxy_pass http://mirrormere-remote:8092;", rendered)
        self.assertIn("proxy_pass http://mirrormere-go2rtc:1984;", rendered)

    def test_custom_env_substitution(self):
        """Validates substitution with custom override values."""
        custom = {
            "PROXY_PORT": "9090",
            "CORE_HOST": "core-internal:8080",
            "REMOTE_HOST": "remote-internal:8092",
            "GO2RTC_HOST": "go2rtc-internal:1984",
        }
        rendered = self.simulate_envsubst(self.template_raw, custom)
        self.assertIn("listen 9090;", rendered)
        self.assertIn("proxy_pass http://core-internal:8080;", rendered)
        self.assertIn("proxy_pass http://remote-internal:8092;", rendered)
        self.assertIn("proxy_pass http://go2rtc-internal:1984;", rendered)

    def test_nginx_internal_variables_preserved(self):
        """Ensures Nginx variables ($host, $1, $remote_addr) are not clobbered by env substitution."""
        defaults = {
            "PROXY_PORT": "8080",
            "CORE_HOST": "mirrormere-core:8080",
            "REMOTE_HOST": "mirrormere-remote:8092",
            "GO2RTC_HOST": "mirrormere-go2rtc:1984",
        }
        rendered = self.simulate_envsubst(self.template_raw, defaults)
        for var in [
            "$host",
            "$remote_addr",
            "$proxy_add_x_forwarded_for",
            "$scheme",
            "$http_upgrade",
            "$connection_upgrade",
            "$1",
        ]:
            self.assertIn(var, rendered, f"Nginx variable {var} was accidentally mutated")


class TestProxyRoutesAndHeaders(unittest.TestCase):
    """Validates routing table, rewrite rules, and header configurations in default.conf.template."""

    def setUp(self):
        self.assertTrue(TEMPLATE_PATH.exists(), f"Missing {TEMPLATE_PATH}")
        self.content = TEMPLATE_PATH.read_text(encoding="utf-8")

    def test_healthz_route(self):
        """Validates exact /healthz probe returning HTTP 200 with logging disabled."""
        body = extract_block(self.content, r"location\s+=\s+/healthz\s*\{")
        self.assertTrue(body, "Could not extract location = /healthz block")
        self.assertIn("access_log off;", body)
        self.assertIn("text/plain", body)
        self.assertIn('return 200 "OK\\n";', body)

    def test_trailing_slash_redirects(self):
        """Validates 301 redirects for /remote, /kiosk/remote, and /kiosk."""
        body_remote = extract_block(self.content, r"location\s+=\s+/remote\s*\{")
        self.assertTrue(body_remote, "Could not extract location = /remote block")
        self.assertIn("return 301 /remote/;", body_remote)

        body_kiosk_remote = extract_block(self.content, r"location\s+=\s+/kiosk/remote\s*\{")
        self.assertTrue(body_kiosk_remote, "Could not extract location = /kiosk/remote block")
        self.assertIn("return 301 /kiosk/remote/;", body_kiosk_remote)

        body_kiosk = extract_block(self.content, r"location\s+=\s+/kiosk\s*\{")
        self.assertTrue(body_kiosk, "Could not extract location = /kiosk block")
        self.assertIn("return 301 /kiosk/;", body_kiosk)

    def test_remote_routes(self):
        """Validates /remote/ and /kiosk/remote/ route rewrites and proxy_pass targets."""
        # /remote/
        remote_body = extract_block(self.content, r"location\s+/remote/\s*\{")
        self.assertTrue(remote_body)
        self.assertIn("rewrite ^/remote/(.*)$ /$1 break;", remote_body)
        self.assertIn("proxy_pass http://${REMOTE_HOST};", remote_body)
        self.assertIn("proxy_buffering off;", remote_body)

        # /kiosk/remote/
        kr_body = extract_block(self.content, r"location\s+/kiosk/remote/\s*\{")
        self.assertTrue(kr_body)
        self.assertIn("rewrite ^/kiosk/remote/(.*)$ /$1 break;", kr_body)
        self.assertIn("proxy_pass http://${REMOTE_HOST};", kr_body)
        self.assertIn("proxy_buffering off;", kr_body)

    def test_kiosk_route(self):
        """Validates /kiosk/ route rewrite rules (/kiosk/$ -> /display, /kiosk/(.*) -> /$1) and SSE/WS settings."""
        body = extract_block(self.content, r"location\s+/kiosk/\s*\{")
        self.assertTrue(body)
        self.assertIn("rewrite ^/kiosk/$ /display break;", body)
        self.assertIn("rewrite ^/kiosk/(.*)$ /$1 break;", body)
        self.assertIn("proxy_pass http://${CORE_HOST};", body)
        self.assertIn("proxy_buffering off;", body)
        self.assertIn("proxy_read_timeout 86400s;", body)
        self.assertIn("proxy_send_timeout 86400s;", body)
        self.assertIn("Upgrade $http_upgrade;", body)
        self.assertIn("Connection $connection_upgrade;", body)

    def test_webrtc_route(self):
        """Validates /webrtc/ route rewrite rule and go2rtc proxy_pass target."""
        body = extract_block(self.content, r"location\s+/webrtc/\s*\{")
        self.assertTrue(body)
        self.assertIn("rewrite ^/webrtc/(.*)$ /$1 break;", body)
        self.assertIn("proxy_pass http://${GO2RTC_HOST};", body)
        self.assertIn("proxy_read_timeout 86400s;", body)
        self.assertIn("proxy_send_timeout 86400s;", body)
        self.assertIn("Upgrade $http_upgrade;", body)
        self.assertIn("Connection $connection_upgrade;", body)

    def test_root_route_no_trailing_slash(self):
        """Validates / catch-all route without trailing slash on proxy_pass."""
        body = extract_block(self.content, r"location\s+/\s*\{")
        self.assertTrue(body)
        self.assertIn("proxy_pass http://${CORE_HOST};", body)
        self.assertNotIn("proxy_pass http://${CORE_HOST}/;", body)
        self.assertIn("proxy_buffering off;", body)
        self.assertIn("proxy_read_timeout 86400s;", body)
        self.assertIn("Upgrade $http_upgrade;", body)
        self.assertIn("Connection $connection_upgrade;", body)

    def test_standard_proxy_headers_present(self):
        """Validates standard reverse-proxy headers in proxied locations."""
        for expected in [
            "proxy_set_header Host $host;",
            "proxy_set_header X-Real-IP $remote_addr;",
            "proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
            "proxy_set_header X-Forwarded-Proto $scheme;",
        ]:
            self.assertIn(expected, self.content)


class TestProxyDockerfile(unittest.TestCase):
    """Validates Dockerfile configuration and build directives."""

    def setUp(self):
        self.assertTrue(DOCKERFILE_PATH.exists(), f"Missing {DOCKERFILE_PATH}")
        self.content = DOCKERFILE_PATH.read_text(encoding="utf-8")

    def test_base_image(self):
        """Verifies official unprivileged nginx 1.27 alpine base image."""
        self.assertIn("FROM nginxinc/nginx-unprivileged:1.27-alpine", self.content)

    def test_file_copies(self):
        """Verifies copying nginx.conf and default.conf.template into proper paths."""
        self.assertIn("COPY nginx.conf /etc/nginx/nginx.conf", self.content)
        self.assertIn("COPY templates/default.conf.template /etc/nginx/templates/default.conf.template", self.content)

    def test_healthcheck(self):
        """Verifies container healthcheck probe via wget against /healthz."""
        self.assertIn("wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1", self.content)


class TestProxyDocumentation(unittest.TestCase):
    """Validates README.md completeness."""

    def setUp(self):
        self.assertTrue(README_PATH.exists(), f"Missing {README_PATH}")
        self.content = README_PATH.read_text(encoding="utf-8")

    def test_required_sections(self):
        """Verifies purpose, architecture, routing table, and environment variables are documented."""
        self.assertIn("Architecture", self.content)
        self.assertIn("Routing Table", self.content)
        self.assertIn("Environment Variables", self.content)
        self.assertIn("PROXY_PORT", self.content)
        self.assertIn("CORE_HOST", self.content)
        self.assertIn("REMOTE_HOST", self.content)
        self.assertIn("GO2RTC_HOST", self.content)


if __name__ == "__main__":
    unittest.main()
