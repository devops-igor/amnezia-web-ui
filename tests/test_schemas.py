"""Test that all schemas can be imported without circular dependencies."""

from pydantic import BaseModel


def test_schemas_importable():
    """All schema classes should be importable from schemas module."""
    import schemas

    model_names = [
        "LoginRequest",
        "AddServerRequest",
        "InstallProtocolRequest",
        "ProtocolRequest",
        "AddConnectionRequest",
        "EditConnectionRequest",
        "ConnectionActionRequest",
        "ToggleConnectionRequest",
        "AddUserRequest",
        "ServerConfigSaveRequest",
        "AppearanceSettings",
        "SyncSettings",
        "CaptchaSettings",
        "SSLSettings",
        "TelegramSettings",
        "ConnectionLimits",
        "ProtocolPaths",
        "UpdateUserRequest",
        "SaveSettingsRequest",
        "ToggleUserRequest",
        "AddUserConnectionRequest",
        "ChangePasswordRequest",
        "ShareSetupRequest",
        "ShareAuthRequest",
        "MyAddConnectionRequest",
    ]
    for name in model_names:
        assert hasattr(schemas, name), f"Missing: {name}"
        cls = getattr(schemas, name)
        assert issubclass(cls, BaseModel)


def test_valid_protocols_importable():
    """VALID_PROTOCOLS should be importable from schemas."""
    import schemas

    assert hasattr(schemas, "VALID_PROTOCOLS")
    assert isinstance(schemas.VALID_PROTOCOLS, set)
