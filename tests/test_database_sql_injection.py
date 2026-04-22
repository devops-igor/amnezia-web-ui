"""
Tests for SQL injection prevention in database.py update methods.
See: tasks/sql-injection-column-names/spec.md
"""

import pytest
import tempfile
import os
from database import Database


class TestUpdateServerSqlInjection:
    """Tests for update_server SQL injection prevention"""

    def setup_method(self):
        """Set up test database"""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        # Create a test server
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.server_id = self.db.get_all_servers()[0]["id"]

    def teardown_method(self):
        """Clean up temporary database"""
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_valid_columns_are_accepted(self):
        """Valid column names should not raise ValueError"""
        # These are all valid server columns
        valid_updates = {
            "name": "Updated Name",
            "host": "new.example.com",
            "ssh_user": "newuser",
            "ssh_port": 2222,
            "ssh_pass": "newpass",
            "protocols": {"awg": {"installed": True, "port": "12345"}},
        }
        # Should not raise
        self.db.update_server(self.server_id, valid_updates)

    def test_malicious_column_name_raises_valueerror(self):
        """SQL injection attempt via column name must raise ValueError"""
        malicious_data = {"name": "legit", "admin'--": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_server(self.server_id, malicious_data)
        assert "admin'--" in str(exc_info.value)
        assert "Unknown server columns" in str(exc_info.value)

    def test_sql_injection_via_union_raises_valueerror(self):
        """UNION-based SQL injection via column name must raise ValueError"""
        malicious_data = {"name": "test", "id FROM users--": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_server(self.server_id, malicious_data)
        assert "Unknown server columns" in str(exc_info.value)

    def test_unknown_column_raises_valueerror(self):
        """Unknown valid-looking column names must raise ValueError"""
        unknown_data = {"name": "test", "nonexistent_column": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_server(self.server_id, unknown_data)
        assert "nonexistent_column" in str(exc_info.value)
        assert "Unknown server columns" in str(exc_info.value)

    def test_multiple_unknown_columns_raises_valueerror_with_all(self):
        """Multiple unknown columns should all be listed in error"""
        malicious_data = {"name": "test", "col1": "v1", "col2": "v2"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_server(self.server_id, malicious_data)
        error_msg = str(exc_info.value)
        assert "col1" in error_msg
        assert "col2" in error_msg


class TestUpdateUserSqlInjection:
    """Tests for update_user SQL injection prevention"""

    def setup_method(self):
        """Set up test database"""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        # Create a test user
        self.db.create_user(
            {
                "id": "test-user-1",
                "username": "testuser",
                "password_hash": "hashed_password",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

    def teardown_method(self):
        """Clean up temporary database"""
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_valid_columns_are_accepted(self):
        """Valid column names should not raise ValueError"""
        valid_updates = {
            "username": "newname",
            "email": "new@example.com",
            "role": "admin",
            "enabled": False,
            "traffic_limit": 1000,
        }
        # Should not raise
        result = self.db.update_user("test-user-1", valid_updates)
        assert result is True

    def test_malicious_column_name_raises_valueerror(self):
        """SQL injection attempt via column name must raise ValueError"""
        malicious_data = {"username": "legit", "admin'--": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_user("test-user-1", malicious_data)
        assert "admin'--" in str(exc_info.value)
        assert "Unknown user columns" in str(exc_info.value)

    def test_sql_injection_via_role_modification_raises_valueerror(self):
        """Attempt to modify role via SQL injection must raise ValueError"""
        malicious_data = {"username": "test", "role": "admin", "admin'--": ""}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_user("test-user-1", malicious_data)
        assert "admin'--" in str(exc_info.value)

    def test_unknown_column_raises_valueerror(self):
        """Unknown valid-looking column names must raise ValueError"""
        unknown_data = {"username": "test", "superuser": True}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_user("test-user-1", unknown_data)
        assert "superuser" in str(exc_info.value)
        assert "Unknown user columns" in str(exc_info.value)

    def test_nonexistent_user_returns_false(self):
        """Updating non-existent user should return False (not raise)"""
        result = self.db.update_user("nonexistent", {"username": "test"})
        assert result is False


class TestUpdateConnectionSqlInjection:
    """Tests for update_connection SQL injection prevention"""

    def setup_method(self):
        """Set up test database"""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        # Create test server and user first
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.server_id = self.db.get_all_servers()[0]["id"]

        self.db.create_user(
            {
                "id": "test-user-1",
                "username": "testuser",
                "password_hash": "hashed_password",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        # Create a test connection
        self.db.create_connection(
            {
                "id": "test-conn-1",
                "user_id": "test-user-1",
                "server_id": self.server_id,
                "protocol": "awg",
                "client_id": "test-client",
                "name": "Test Connection",
            }
        )

    def teardown_method(self):
        """Clean up temporary database"""
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_valid_columns_are_accepted(self):
        """Valid column names should not raise ValueError"""
        valid_updates = {
            "name": "Updated Connection",
            "last_rx": 1000,
            "last_tx": 2000,
        }
        # Should not raise
        result = self.db.update_connection("test-conn-1", valid_updates)
        assert result is True

    def test_malicious_column_name_raises_valueerror(self):
        """SQL injection attempt via column name must raise ValueError"""
        malicious_data = {"name": "legit", "admin'--": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_connection("test-conn-1", malicious_data)
        assert "admin'--" in str(exc_info.value)
        assert "Unknown connection columns" in str(exc_info.value)

    def test_sql_injection_via_set_clause_raises_valueerror(self):
        """SET clause manipulation via column name must raise ValueError"""
        malicious_data = {"name": "test", "server_id = 1 WHERE 1=1--": "value"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_connection("test-conn-1", malicious_data)
        assert "Unknown connection columns" in str(exc_info.value)

    def test_unknown_column_raises_valueerror(self):
        """Unknown valid-looking column names must raise ValueError"""
        unknown_data = {"name": "test", "remote_code": "exec('rm -rf')"}
        with pytest.raises(ValueError) as exc_info:
            self.db.update_connection("test-conn-1", unknown_data)
        assert "remote_code" in str(exc_info.value)
        assert "Unknown connection columns" in str(exc_info.value)

    def test_nonexistent_connection_returns_false(self):
        """Updating non-existent connection should return False (not raise)"""
        result = self.db.update_connection("nonexistent", {"name": "test"})
        assert result is False

    def test_empty_updates_returns_true(self):
        """Empty updates dict should return True (no-op)"""
        result = self.db.update_connection("test-conn-1", {})
        assert result is True
