"""
Tests for rate limiting on login and share endpoints.
"""

from unittest.mock import MagicMock, patch
from slowapi import Limiter
from slowapi.errors import RateLimitExceeded


class TestGetClientIp:
    """Tests for the _get_client_ip helper function."""

    def test_direct_connection(self):
        """Without X-Forwarded-For, falls back to remote address."""
        from app import _get_client_ip

        request = MagicMock()
        request.headers.get.return_value = None
        request.client.host = "10.0.0.1"
        result = _get_client_ip(request)
        assert result == "10.0.0.1"

    def test_x_forwarded_for_single(self):
        """With single X-Forwarded-For from a trusted proxy, uses that IP."""
        from unittest.mock import patch
        import ipaddress
        from app import _get_client_ip
        from app.utils import helpers

        trusted = {ipaddress.IPv4Address("10.0.0.1")}
        with (
            patch.object(helpers, "_trusted_proxy_hosts", trusted),
            patch.object(helpers, "_trusted_proxy_networks", []),
        ):
            request = MagicMock()
            request.headers.get.return_value = "1.2.3.4"
            request.client.host = "10.0.0.1"
            result = _get_client_ip(request)
            assert result == "1.2.3.4"

    def test_x_forwarded_for_chain(self):
        """With chained X-Forwarded-For, uses the first (original client) IP."""
        from unittest.mock import patch
        import ipaddress
        from app import _get_client_ip
        from app.utils import helpers

        trusted = {ipaddress.IPv4Address("10.0.0.1")}
        with (
            patch.object(helpers, "_trusted_proxy_hosts", trusted),
            patch.object(helpers, "_trusted_proxy_networks", []),
        ):
            request = MagicMock()
            request.headers.get.return_value = "1.2.3.4, 10.0.0.1, 10.0.0.2"
            request.client.host = "10.0.0.1"
            result = _get_client_ip(request)
            assert result == "1.2.3.4"

    def test_x_forwarded_for_with_extra_spaces(self):
        """Handles spaces around commas in X-Forwarded-For."""
        from unittest.mock import patch
        import ipaddress
        from app import _get_client_ip
        from app.utils import helpers

        trusted = {ipaddress.IPv4Address("10.0.0.1")}
        with (
            patch.object(helpers, "_trusted_proxy_hosts", trusted),
            patch.object(helpers, "_trusted_proxy_networks", []),
        ):
            request = MagicMock()
            request.headers.get.return_value = "  1.2.3.4 , 10.0.0.1 "
            request.client.host = "10.0.0.1"
            result = _get_client_ip(request)
            assert result == "1.2.3.4"

    def test_x_forwarded_for_untrusted_proxy(self):
        """When peer is NOT in TRUSTED_PROXIES, X-Forwarded-For is ignored."""
        from unittest.mock import patch
        import ipaddress
        from app.utils import helpers

        request = MagicMock()
        request.client.host = "10.0.0.1"
        request.headers.get.return_value = "1.2.3.4"

        trusted = {ipaddress.IPv4Address("172.16.0.1")}
        with (
            patch.object(helpers, "_trusted_proxy_hosts", trusted),
            patch.object(helpers, "_trusted_proxy_networks", []),
        ):
            result = helpers._get_client_ip(request)
        assert result == "10.0.0.1"


class TestRateLimitExceededHandler:
    """Tests for the custom rate limit exceeded handler."""

    def test_handler_returns_429(self):
        """Handler returns 429 status with i18n error message."""
        import asyncio
        from app import _rate_limit_exceeded_handler

        request = MagicMock()
        request.method = "POST"
        request.url = MagicMock()
        request.url.path = "/api/auth/login"
        request.cookies = {"lang": "en"}
        request.headers.get.return_value = None
        request.client.host = "127.0.0.1"

        limit_mock = MagicMock()
        limit_mock.error_message = None
        limit_mock.limit = "5 per 1 minute"
        exc = RateLimitExceeded(limit_mock)
        response = asyncio.get_event_loop().run_until_complete(
            _rate_limit_exceeded_handler(request, exc)
        )
        assert response.status_code == 429

    def test_handler_returns_i18n_message(self):
        """Handler uses _t() for error message."""
        import asyncio
        from app import _rate_limit_exceeded_handler

        request = MagicMock()
        request.method = "GET"
        request.url = MagicMock()
        request.url.path = "/api/share/test"
        request.cookies = {"lang": "en"}
        request.headers.get.return_value = None
        request.client.host = "127.0.0.1"

        limit_mock = MagicMock()
        limit_mock.error_message = None
        limit_mock.limit = "10 per 1 minute"
        exc = RateLimitExceeded(limit_mock)
        response = asyncio.get_event_loop().run_until_complete(
            _rate_limit_exceeded_handler(request, exc)
        )
        body = response.body
        if isinstance(body, bytes):
            import json

            data = json.loads(body)
        else:
            data = body
        assert "error" in data


class TestLimiterInitialization:
    """Tests for rate limiter being properly configured on the app."""

    def test_limiter_exists_on_app_state(self):
        """limiter is attached to app.state.limiter."""
        from app import app

        assert hasattr(app.state, "limiter")
        assert isinstance(app.state.limiter, Limiter)

    def test_exception_handler_registered(self):
        """RateLimitExceeded exception handler is registered."""
        from app import app

        assert RateLimitExceeded in app.exception_handlers


class TestLoginRateLimit:
    """Integration tests for login endpoint rate limiting."""

    def test_login_rate_limit_triggers(self, csrf_client):
        """After 5 login attempts, the 6th returns 429."""
        for i in range(5):
            resp = csrf_client.post(
                "/api/auth/login",
                json={"username": "test", "password": "***"},
            )
            # All should be 401 (bad credentials), not 429
            assert resp.status_code in (
                400,
                401,
            ), f"Request {i + 1} returned {resp.status_code}, expected 400 or 401"

        # 6th request should hit rate limit
        resp = csrf_client.post(
            "/api/auth/login",
            json={"username": "test", "password": "***"},
        )
        assert resp.status_code == 429, f"Expected 429, got {resp.status_code}"

    def test_login_rate_limit_error_message(self, csrf_client):
        """429 response contains the rate_limit_exceeded translation key."""
        # Exhaust the rate limit
        for i in range(5):
            csrf_client.post(
                "/api/auth/login",
                json={"username": "test", "password": "***"},
            )

        resp = csrf_client.post(
            "/api/auth/login",
            json={"username": "test", "password": "***"},
        )
        assert resp.status_code == 429
        data = resp.json()
        assert "error" in data


class TestSharePageRateLimit:
    """Integration tests for share page rate limiting."""

    def test_share_page_rate_limit_triggers(self, csrf_client):
        """Share page returns 429 after 10 requests/minute."""
        for i in range(10):
            resp = csrf_client.get("/share/sometoken")
            # May get 404 (token not found) — that is fine, we are testing rate limit
            assert resp.status_code in (200, 404), f"Request {i + 1} returned {resp.status_code}"

        # 11th request should hit rate limit
        resp = csrf_client.get("/share/sometoken")
        assert resp.status_code == 429, f"Expected 429, got {resp.status_code}"


class TestTrustedProxiesCidr:
    """Tests for CIDR support in TRUSTED_PROXIES (helpers._get_client_ip)."""

    def test_cidr_match(self):
        """Peer IP inside a trusted CIDR network gets X-Forwarded-For honoured."""
        from unittest.mock import patch
        import ipaddress
        from app.utils import helpers

        network = ipaddress.ip_network("172.18.0.0/24", strict=False)
        with (
            patch.object(helpers, "_trusted_proxy_hosts", set()),
            patch.object(helpers, "_trusted_proxy_networks", [network]),
        ):
            request = MagicMock()
            request.headers.get.return_value = "1.2.3.4"
            request.client.host = "172.18.0.5"
            result = helpers._get_client_ip(request)
            assert result == "1.2.3.4"

    def test_cidr_no_match(self):
        """Peer IP outside any trusted network ignores X-Forwarded-For."""
        from unittest.mock import patch
        import ipaddress
        from app.utils import helpers

        network = ipaddress.ip_network("192.168.0.0/24", strict=False)
        with (
            patch.object(helpers, "_trusted_proxy_hosts", set()),
            patch.object(helpers, "_trusted_proxy_networks", [network]),
        ):
            request = MagicMock()
            request.headers.get.return_value = "1.2.3.4"
            request.client.host = "10.0.0.1"
            result = helpers._get_client_ip(request)
            assert result == "10.0.0.1"

    def test_mixed_ip_and_cidr(self):
        """Mixing exact IPs and CIDR networks both work."""
        from unittest.mock import patch
        import ipaddress
        from app.utils import helpers

        host_ip = ipaddress.IPv4Address("10.0.0.1")
        network = ipaddress.ip_network("172.18.0.0/24", strict=False)
        with (
            patch.object(helpers, "_trusted_proxy_hosts", {host_ip}),
            patch.object(helpers, "_trusted_proxy_networks", [network]),
        ):
            # Exact IP match
            req1 = MagicMock()
            req1.headers.get.return_value = "1.2.3.4"
            req1.client.host = "10.0.0.1"
            assert helpers._get_client_ip(req1) == "1.2.3.4"

            # CIDR match
            req2 = MagicMock()
            req2.headers.get.return_value = "5.6.7.8"
            req2.client.host = "172.18.0.99"
            assert helpers._get_client_ip(req2) == "5.6.7.8"

            # No match
            req3 = MagicMock()
            req3.headers.get.return_value = "9.9.9.9"
            req3.client.host = "192.168.1.1"
            assert helpers._get_client_ip(req3) == "192.168.1.1"

    def test_invalid_entry_skipped_with_warning(self):
        """Invalid TRUSTED_PROXIES entry logs warning, does not crash."""
        from unittest.mock import patch
        from app.utils import helpers

        with patch.object(helpers.logger, "warning") as mock_warn:
            helpers._parse_trusted_proxies("not-an-ip, 10.0.0.1")
            mock_warn.assert_called_once()
            args = mock_warn.call_args[0]
            # The warning should mention the bad entry
            assert "not-an-ip" in args[0] or "not-an-ip" in str(args)

    def test_ipv6_cidr(self):
        """IPv6 CIDR network correctly matches IPv6 peer address."""
        from unittest.mock import patch
        import ipaddress
        from app.utils import helpers

        network = ipaddress.ip_network("fd00::/64", strict=False)
        with (
            patch.object(helpers, "_trusted_proxy_hosts", set()),
            patch.object(helpers, "_trusted_proxy_networks", [network]),
        ):
            request = MagicMock()
            request.headers.get.return_value = "2001:db8::1"
            request.client.host = "fd00::1"
            result = helpers._get_client_ip(request)
            assert result == "2001:db8::1"
