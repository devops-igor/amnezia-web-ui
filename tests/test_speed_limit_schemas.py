"""Tests for speed limit schema fields and validation."""

import pytest
from pydantic import ValidationError

from schemas import (
    AddConnectionRequest,
    EditConnectionRequest,
    MyAddConnectionRequest,
    SpeedLimitRequest,
)


class TestSpeedLimitSchemaValidation:
    """Test speed_limit field validation in connection request schemas."""

    # ---- SpeedLimitRequest tests ----

    def test_speed_limit_request_all_null(self):
        req = SpeedLimitRequest()
        assert req.speed_limit_down is None
        assert req.speed_limit_up is None

    def test_speed_limit_request_positive_integers(self):
        req = SpeedLimitRequest(speed_limit_down=100, speed_limit_up=50)
        assert req.speed_limit_down == 100
        assert req.speed_limit_up == 50

    def test_speed_limit_request_zero_means_unlimited(self):
        req = SpeedLimitRequest(speed_limit_down=0, speed_limit_up=0)
        assert req.speed_limit_down == 0
        assert req.speed_limit_up == 0

    def test_speed_limit_request_partial_null(self):
        req = SpeedLimitRequest(speed_limit_down=50)
        assert req.speed_limit_down == 50
        assert req.speed_limit_up is None

    def test_speed_limit_request_negative_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            SpeedLimitRequest(speed_limit_down=-10)
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)

    def test_speed_limit_request_negative_up_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            SpeedLimitRequest(speed_limit_up=-5)
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)

    # ---- AddConnectionRequest tests ----

    def test_add_connection_request_with_speed_limits(self):
        req = AddConnectionRequest(
            protocol="awg",
            name="Test",
            awg_speed_limit_down=100,
            awg_speed_limit_up=50,
        )
        assert req.awg_speed_limit_down == 100
        assert req.awg_speed_limit_up == 50

    def test_add_connection_request_null_speed_limits(self):
        req = AddConnectionRequest(protocol="awg", name="Test")
        assert req.awg_speed_limit_down is None
        assert req.awg_speed_limit_up is None

    def test_add_connection_request_zero_speed_limits(self):
        req = AddConnectionRequest(
            protocol="awg", name="Test", awg_speed_limit_down=0, awg_speed_limit_up=0
        )
        assert req.awg_speed_limit_down == 0
        assert req.awg_speed_limit_up == 0

    def test_add_connection_request_negative_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            AddConnectionRequest(protocol="awg", name="Test", awg_speed_limit_down=-1)
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)

    def test_add_connection_request_negative_up_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            AddConnectionRequest(protocol="awg", name="Test", awg_speed_limit_up=-1)
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)

    # ---- EditConnectionRequest tests ----

    def test_edit_connection_request_with_speed_limits(self):
        req = EditConnectionRequest(
            protocol="awg",
            client_id="abc123",
            awg_speed_limit_down=200,
            awg_speed_limit_up=100,
        )
        assert req.awg_speed_limit_down == 200
        assert req.awg_speed_limit_up == 100

    def test_edit_connection_request_null_speed_limits(self):
        req = EditConnectionRequest(protocol="awg", client_id="abc123")
        assert req.awg_speed_limit_down is None
        assert req.awg_speed_limit_up is None

    def test_edit_connection_request_negative_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            EditConnectionRequest(protocol="awg", client_id="abc123", awg_speed_limit_up=-50)
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)

    # ---- MyAddConnectionRequest tests ----

    def test_my_add_connection_request_with_speed_limits(self):
        req = MyAddConnectionRequest(
            server_id=1,
            protocol="awg",
            name="Test",
            awg_speed_limit_down=75,
            awg_speed_limit_up=30,
        )
        assert req.awg_speed_limit_down == 75
        assert req.awg_speed_limit_up == 30

    def test_my_add_connection_request_null_speed_limits(self):
        req = MyAddConnectionRequest(server_id=1, protocol="awg", name="Test")
        assert req.awg_speed_limit_down is None
        assert req.awg_speed_limit_up is None

    def test_my_add_connection_request_negative_rejected(self):
        with pytest.raises(ValidationError) as exc_info:
            MyAddConnectionRequest(
                server_id=1, protocol="awg", name="Test", awg_speed_limit_down=-10
            )
        assert "greater_than_equal" in str(exc_info.value)
        assert "0" in str(exc_info.value)
