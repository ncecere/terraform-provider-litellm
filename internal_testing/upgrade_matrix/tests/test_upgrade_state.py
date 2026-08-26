import hashlib
import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / "upgrade_state.py"
SPEC = importlib.util.spec_from_file_location("upgrade_state", MODULE_PATH)
assert SPEC and SPEC.loader
upgrade_state = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(upgrade_state)


def attribute(type_shape, *, sensitive=False):
    result = {"type": type_shape}
    if sensitive:
        result["sensitive"] = True
    return result


def nested_attribute(mode, attributes, *, sensitive=False):
    result = {"nested_type": {"nesting_mode": mode, "attributes": attributes}}
    if sensitive:
        result["sensitive"] = True
    return result


def nested_block(mode, attributes=None, block_types=None):
    return {
        "nesting_mode": mode,
        "block": {"attributes": attributes or {}, "block_types": block_types or {}},
    }


def provider_schema():
    return {
        "resource_schemas": {
            "litellm_agent": {
                "version": 0,
                "block": {
                    "attributes": {
                        "id": attribute("string"),
                        "agent_name": attribute("string"),
                        "profile": nested_attribute("single", {
                            "public_label": attribute("string"),
                            "secret": attribute("string", sensitive=True),
                            "children": nested_attribute("list", {
                                "visible": attribute("string"),
                                "credential": attribute("string", sensitive=True),
                            }),
                        }),
                    },
                    "block_types": {
                        "agent_card": nested_block("single", {
                            "name": attribute("string"),
                            "url": attribute("string"),
                        }, {
                            "skills": nested_block("list", {
                                "id": attribute("string"),
                                "description": attribute("string"),
                                "private_hint": attribute("string", sensitive=True),
                            }, {
                                "aliases": nested_block("set", {"value": attribute("string")}),
                                "routes": nested_block("map", {"target": attribute("string")}),
                            }),
                        }),
                    },
                },
            },
            "litellm_team_member_add": {
                "version": 0,
                "block": {
                    "attributes": {
                        "id": attribute("string"),
                        "team_id": attribute("string"),
                    },
                    "block_types": {
                        "member": nested_block("set", {
                            "user_id": attribute("string"),
                            "user_email": attribute("string"),
                            "role": attribute("string"),
                        }),
                    },
                },
            },
        },
    }


def resource(address, resource_type, values, schema_version=0):
    return {
        "address": address,
        "mode": "managed",
        "type": resource_type,
        "name": address.split(".")[-1],
        "provider_name": upgrade_state.PROVIDER_SOURCE,
        "schema_version": schema_version,
        "values": values,
    }


def state(*resources):
    return {"values": {"root_module": {"resources": list(resources)}}}


def agent_values():
    return {
        "id": "agent-id",
        "agent_name": "review-agent",
        "profile": {
            "public_label": "visible",
            "secret": "nested-secret-before",
            "children": [{"visible": "sibling", "credential": "child-secret-before"}],
        },
        "agent_card": {
            "name": "Agent",
            "url": "https://agent.invalid",
            "skills": [{
                "id": "chat",
                "description": "original",
                "private_hint": "block-secret-before",
                "aliases": [{"value": "second"}, {"value": "first"}],
                "routes": {"primary": {"target": "backend-a"}},
            }],
        },
    }


def members_values():
    return {
        "id": "team-id",
        "team_id": "team-id",
        "member": [
            {"user_id": "user-b", "user_email": "b@example.invalid", "role": "user"},
            {"user_id": "user-a", "user_email": "a@example.invalid", "role": "admin"},
        ],
    }


class UpgradeStateTests(unittest.TestCase):
    def compare(self, before, after):
        return upgrade_state.compare_state_values(before, after, provider_schema(), {})

    def test_agent_card_nested_leaf_mutation_is_detected_for_every_block_mode(self):
        def mutate(values, mode):
            if mode == "single":
                values["agent_card"]["name"] = "Mutated Agent"
            elif mode == "list":
                values["agent_card"]["skills"][0]["description"] = "mutated"
            elif mode == "set":
                values["agent_card"]["skills"][0]["aliases"][0]["value"] = "mutated"
            else:
                values["agent_card"]["skills"][0]["routes"]["primary"]["target"] = "backend-b"

        for mode in ("single", "list", "set", "map"):
            before_values = agent_values()
            after_values = agent_values()
            mutate(after_values, mode)
            with self.subTest(mode=mode), self.assertRaisesRegex(
                upgrade_state.UpgradeStateError, "agent_card"
            ):
                self.compare(
                    state(resource("litellm_agent.test", "litellm_agent", before_values)),
                    state(resource("litellm_agent.test", "litellm_agent", after_values)),
                )

    def test_team_member_add_member_mutation_is_detected(self):
        before_values = members_values()
        after_values = members_values()
        after_values["member"][0]["role"] = "admin"
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "member"):
            self.compare(
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
            )

    def test_set_reordering_is_semantic_but_list_reordering_is_not(self):
        before_values = members_values()
        after_values = members_values()
        after_values["member"].reverse()
        self.assertFalse(self.compare(
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
        ))

        before_agent = agent_values()
        after_agent = agent_values()
        before_agent["agent_card"]["skills"].append({
            "id": "other", "description": "second", "private_hint": "secret",
            "aliases": [], "routes": {},
        })
        after_agent["agent_card"]["skills"].append({
            "id": "other", "description": "second", "private_hint": "different",
            "aliases": [], "routes": {},
        })
        after_agent["agent_card"]["skills"].reverse()
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "agent_card"):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", before_agent)),
                state(resource("litellm_agent.test", "litellm_agent", after_agent)),
            )

    def test_sensitive_nested_leaves_are_excluded_without_hiding_siblings(self):
        before_values = agent_values()
        canonical = upgrade_state.canonicalize_resources(
            state(resource("litellm_agent.test", "litellm_agent", before_values)),
            provider_schema(),
        )["litellm_agent.test"]["values"]
        self.assertEqual(canonical["profile"]["public_label"], "visible")
        self.assertNotIn("secret", canonical["profile"])
        self.assertEqual(canonical["profile"]["children"][0]["visible"], "sibling")
        self.assertNotIn("credential", canonical["profile"]["children"][0])
        self.assertNotIn("private_hint", canonical["agent_card"]["skills"][0])
        rendered = repr(canonical)
        for secret in ("nested-secret-before", "child-secret-before", "block-secret-before"):
            self.assertNotIn(secret, rendered)

        secret_only = agent_values()
        secret_only["profile"]["secret"] = "nested-secret-after"
        secret_only["profile"]["children"][0]["credential"] = "child-secret-after"
        secret_only["agent_card"]["skills"][0]["private_hint"] = "block-secret-after"
        self.assertFalse(self.compare(
            state(resource("litellm_agent.test", "litellm_agent", before_values)),
            state(resource("litellm_agent.test", "litellm_agent", secret_only)),
        ))

        public_changed = agent_values()
        public_changed["profile"]["children"][0]["visible"] = "changed"
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "profile"):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", before_values)),
                state(resource("litellm_agent.test", "litellm_agent", public_changed)),
            )

    def test_absent_current_schema_values_use_typed_semantic_absence(self):
        absent = agent_values()
        absent.pop("profile")
        absent["agent_card"]["skills"][0].pop("aliases")
        absent["agent_card"]["skills"][0].pop("routes")
        explicit = agent_values()
        explicit["profile"] = None
        explicit["agent_card"]["skills"][0]["aliases"] = []
        explicit["agent_card"]["skills"][0]["routes"] = {}
        self.assertFalse(self.compare(
            state(resource("litellm_agent.test", "litellm_agent", absent)),
            state(resource("litellm_agent.test", "litellm_agent", explicit)),
        ))
        canonical = upgrade_state.canonicalize_resources(
            state(resource("litellm_agent.test", "litellm_agent", absent)), provider_schema()
        )["litellm_agent.test"]["values"]
        self.assertIsInstance(canonical["profile"], upgrade_state.TypedAbsence)
        self.assertIn("nested-attribute", canonical["profile"].shape)
        self.assertIsInstance(
            canonical["agent_card"]["skills"][0]["aliases"], upgrade_state.TypedAbsence
        )
        self.assertIn("set", canonical["agent_card"]["skills"][0]["aliases"].shape)

    def test_reviewed_migration_exceptions_are_retained(self):
        before_values = agent_values()
        after_values = agent_values()
        after_values["agent_name"] = "reviewed-computed-value"
        self.assertTrue(upgrade_state.compare_state_values(
            state(resource("litellm_agent.test", "litellm_agent", before_values, 0)),
            state(resource("litellm_agent.test", "litellm_agent", after_values, 1)),
            provider_schema(),
            {
                "upgrade_expected_computed_migrations": {"litellm_agent": ["agent_name"]},
                "upgrade_expected_schema_migrations": {"litellm_agent": [0, 1]},
            },
        ))

        identity_after = agent_values()
        identity_after["id"] = "sha256:" + hashlib.sha256(b"agent-id").hexdigest()
        self.assertTrue(upgrade_state.compare_state_values(
            state(resource("litellm_agent.test", "litellm_agent", agent_values())),
            state(resource("litellm_agent.test", "litellm_agent", identity_after)),
            provider_schema(),
            {"upgrade_expected_identity_migrations": {"litellm_agent": "sha256-of-prior-id"}},
        ))

    def test_malformed_state_and_schema_shapes_fail_closed(self):
        malformed = members_values()
        malformed["member"] = {"not": "a set array"}
        with self.assertRaises(upgrade_state.UpgradeStateError):
            upgrade_state.canonicalize_resources(
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", malformed)),
                provider_schema(),
            )

        bad_schema = provider_schema()
        bad_schema["resource_schemas"]["litellm_team_member_add"]["block"]["block_types"]["member"]["nesting_mode"] = "group"
        with self.assertRaises(upgrade_state.UpgradeStateError):
            upgrade_state.canonicalize_resources(
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", members_values())),
                bad_schema,
            )

        hidden_malformed = agent_values()
        hidden_malformed["profile"] = "not-an-object"
        with self.assertRaises(upgrade_state.UpgradeStateError):
            upgrade_state.canonicalize_resources(
                state(resource("litellm_agent.test", "litellm_agent", hidden_malformed)),
                provider_schema(),
            )


if __name__ == "__main__":
    unittest.main()
