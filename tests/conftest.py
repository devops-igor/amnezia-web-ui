"""
Pytest configuration and fixtures for Amnezia Web Panel tests.
"""

import pytest


@pytest.fixture
def csrf_client():
    """Provides a TestClient that handles CSRF tokens.

    The starlette-csrf middleware sets a signed csrftoken cookie on every response.
    For unsafe methods (POST, PUT, DELETE), it validates both the cookie and header.

    TestClient does not automatically persist cookies from Set-Cookie headers,
    so we extract the token manually and patch the client methods to include it.
    """
    from app import app
    from fastapi.testclient import TestClient

    client = TestClient(app)

    # Make initial GET to obtain CSRF cookie
    # IMPORTANT: Don't follow redirects, as the Set-Cookie header is lost
    response = client.get("/", follow_redirects=False)

    # Extract cookie from raw Set-Cookie header(s)
    # starlette-csrf sets cookie on EVERY response, signed with URLSafeSerializer
    csrf_token = None
    for header_value in response.headers.get_list("set-cookie"):
        if "csrftoken=" in header_value:
            # Parse: csrftoken=VALUE; Path=/; SameSite=lax
            csrf_token = header_value.split("csrftoken=")[1].split(";")[0]
            break

    if csrf_token:
        # Set the cookie on the client so it's sent with all subsequent requests
        client.cookies.set("csrftoken", csrf_token)

    # Monkey-patch the post method to auto-include the header
    original_post = client.post

    def csrf_post(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_post(url, **kwargs)

    client.post = csrf_post

    # Same for put and delete
    original_put = client.put

    def csrf_put(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_put(url, **kwargs)

    client.put = csrf_put

    original_delete = client.delete

    def csrf_delete(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_delete(url, **kwargs)

    client.delete = csrf_delete

    yield client


def create_csrf_client():
    """Factory function to create a CSRF-aware TestClient.

    Use this when you need to create a client inside a test method
    rather than as a fixture argument.
    """
    from app import app
    from fastapi.testclient import TestClient

    client = TestClient(app)

    # Make initial GET to obtain CSRF cookie
    # IMPORTANT: Don't follow redirects, as the Set-Cookie header is lost
    response = client.get("/", follow_redirects=False)

    # Extract cookie from raw Set-Cookie header(s)
    csrf_token = None
    for header_value in response.headers.get_list("set-cookie"):
        if "csrftoken=" in header_value:
            csrf_token = header_value.split("csrftoken=")[1].split(";")[0]
            break

    if csrf_token:
        client.cookies.set("csrftoken", csrf_token)

    # Monkey-patch methods to auto-include CSRF header
    original_post = client.post

    def csrf_post(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_post(url, **kwargs)

    client.post = csrf_post

    original_put = client.put

    def csrf_put(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_put(url, **kwargs)

    client.put = csrf_put

    original_delete = client.delete

    def csrf_delete(url, **kwargs):
        headers = kwargs.pop("headers", {})
        if csrf_token:
            headers["x-csrftoken"] = csrf_token
        kwargs["headers"] = headers
        return original_delete(url, **kwargs)

    client.delete = csrf_delete

    return client
