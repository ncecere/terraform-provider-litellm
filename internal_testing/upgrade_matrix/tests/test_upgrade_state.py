import hashlib
import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / "upgrade_state.py"
SPEC = importlib.util.spec_from_file_location("upgrade_state", MODULE_PATH)
assert SPEC and SPEC.loader
upgrade_state = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(upgrade_state)


def attribute(type_shape, *, sensitive=False, computed=False, optional=False):
    result = {"type": type_shape}
    if sensitive:
        result["sensitive"] = True
    if computed:
        result["computed"] = True
    if optional:
        result["optional"] = True
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
                        "id": attribute("string", computed=True),
                        "agent_name": attribute("string", computed=True),
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
                            "supports_authenticated_extended_card": attribute("bool", optional=True),
                        }, {
                            "signatures": nested_block("list", {
                                "protected": attribute("string", sensitive=True),
                                "signature": attribute("string", sensitive=True),
                            }),
                            "skills": nested_block("list", {
                                "id": attribute("string"),
                                "description": attribute("string"),
                                "private_hint": attribute("string", sensitive=True),
                            }, {
                                "aliases": nested_block("set", {"value": attribute("string")}),
                                "routes": nested_block("map", {"target": attribute("string", computed=True)}),
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
                            "user_id": attribute("string", computed=True),
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


def agent_private_trigger_matrix():
    return {
        "upgrade_expected_private_migrations": ["litellm_agent"],
        "upgrade_expected_private_plan_triggers": {"litellm_agent": ["id"]},
    }


def agent_plan_change(*, actions=None, before=None, after=None, unknown=None,
                      before_sensitive=None, after_sensitive=None):
    return {"resource_changes": [{
        "address": "litellm_agent.test",
        "mode": "managed",
        "type": "litellm_agent",
        "name": "test",
        "change": {
            "actions": ["update"] if actions is None else actions,
            "before": before if before is not None else agent_values(),
            "after": after if after is not None else {**agent_values(), "id": None},
            "after_unknown": unknown if unknown is not None else {"id": True},
            "before_sensitive": before_sensitive or {},
            "after_sensitive": after_sensitive or {},
        },
    }]}


def raw_private(resource_type="litellm_agent", private=""):
    return {"resources": [{
        "type": resource_type,
        "name": "test",
        "instances": [{"private": private}],
    }]}


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
    def compare(self, before, after, matrix=None, schema=None, *, exact_public=False):
        return upgrade_state.compare_state_values(
            before, after, schema or provider_schema(), matrix or {},
            exact_public=exact_public,
        )

    @staticmethod
    def member_migration_matrix(*paths):
        return {
            "upgrade_expected_computed_migrations": {
                "litellm_team_member_add": list(paths or ("member[*].user_id",))
            }
        }

    def test_reviewed_private_plan_trigger_accepts_exact_agent_unknown_identity(self):
        triggered = upgrade_state.review_upgrade_plan(
            agent_plan_change(), provider_schema(), agent_private_trigger_matrix()
        )
        self.assertEqual(triggered, {"litellm_agent"})
        baseline = upgrade_state.private_trigger_plan_baseline(
            agent_plan_change(), provider_schema(), agent_private_trigger_matrix(),
            "litellm_agent",
        )
        self.assertEqual(
            baseline["values"]["root_module"]["resources"][0]["values"],
            agent_values(),
        )
        omitted = agent_values()
        omitted.pop("id")
        self.assertEqual(
            upgrade_state.review_upgrade_plan(
                agent_plan_change(after=omitted), provider_schema(),
                agent_private_trigger_matrix(),
            ),
            {"litellm_agent"},
        )

    def test_private_plan_trigger_contract_rejects_every_schema_boundary(self):
        cases = []
        for paths in ([], ["profile.id"], ["id", "id"]):
            cases.append(("path", paths, None))
        cases.extend([
            ("not-private", ["id"], None),
            ("not-computed", ["id"], lambda meta: meta.pop("computed")),
            ("sensitive", ["id"], lambda meta: meta.update({"sensitive": True})),
            ("non-string", ["id"], lambda meta: meta.update({"type": "number"})),
        ])
        for name, paths, mutate in cases:
            schema = provider_schema()
            matrix = agent_private_trigger_matrix()
            matrix["upgrade_expected_private_plan_triggers"]["litellm_agent"] = paths
            if name == "not-private":
                matrix["upgrade_expected_private_migrations"] = []
            elif mutate:
                identity = schema["resource_schemas"]["litellm_agent"]["block"]["attributes"]["id"]
                mutate(identity)
            with self.subTest(name=name), self.assertRaises(upgrade_state.UpgradeStateError):
                upgrade_state.compile_upgrade_contract(schema, matrix)
        matrix = agent_private_trigger_matrix()
        matrix["upgrade_expected_private_plan_triggers"] = {"litellm_missing": ["id"]}
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "absent"):
            upgrade_state.compile_upgrade_contract(provider_schema(), matrix)

    def test_private_plan_trigger_never_weakens_ordinary_identity_masks(self):
        matrix = agent_private_trigger_matrix()
        matrix["upgrade_expected_computed_migrations"] = {"litellm_agent": ["id"]}
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "identity"):
            upgrade_state.compile_upgrade_contract(provider_schema(), matrix)

    def test_private_plan_trigger_rejects_every_action_boundary(self):
        for actions in (["create"], ["delete"], ["create", "delete"],
                        ["delete", "create"], ["no-op"], []):
            with self.subTest(actions=actions), self.assertRaises(upgrade_state.UpgradeStateError):
                upgrade_state.review_upgrade_plan(
                    agent_plan_change(actions=actions), provider_schema(),
                    agent_private_trigger_matrix(),
                )

    def test_private_plan_trigger_rejects_unknown_prior_and_known_or_null_after_identity(self):
        for prior in (None, "", "   "):
            before = agent_values()
            before["id"] = prior
            with self.subTest(prior=repr(prior)), self.assertRaisesRegex(
                upgrade_state.UpgradeStateError, "prior identity"
            ):
                upgrade_state.review_upgrade_plan(
                    agent_plan_change(before=before), provider_schema(),
                    agent_private_trigger_matrix(),
                )
        for proposed, unknown in (("changed", {"id": True}),
                                  (None, {}), (None, {"id": False})):
            after = agent_values()
            after["id"] = proposed
            with self.subTest(proposed=repr(proposed), unknown=unknown), self.assertRaises(
                upgrade_state.UpgradeStateError
            ):
                upgrade_state.review_upgrade_plan(
                    agent_plan_change(after=after, unknown=unknown), provider_schema(),
                    agent_private_trigger_matrix(),
                )

    def test_private_plan_trigger_rejects_sensitive_extra_and_nested_unknowns(self):
        extra = {**agent_values(), "id": None, "agent_name": "changed"}
        variants = [
            agent_plan_change(after=extra),
            agent_plan_change(after_sensitive={"id": True}),
            agent_plan_change(before_sensitive={"id": True}),
            agent_plan_change(unknown={"id": True, "profile": {"public_label": True}}),
            agent_plan_change(unknown={"profile": {"id": True}}),
        ]
        for index, plan in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(upgrade_state.UpgradeStateError):
                upgrade_state.review_upgrade_plan(
                    plan, provider_schema(), agent_private_trigger_matrix()
                )

    def test_private_plan_trigger_rejects_unreviewed_type_and_known_identity_change(self):
        unreviewed = agent_private_trigger_matrix()
        unreviewed["upgrade_expected_private_plan_triggers"] = {}
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "unreviewed identity"):
            upgrade_state.review_upgrade_plan(
                agent_plan_change(), provider_schema(), unreviewed
            )
        after = agent_values()
        after["id"] = "known-change"
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "identity changed"):
            upgrade_state.review_upgrade_plan(
                agent_plan_change(after=after, unknown={}), provider_schema(),
                agent_private_trigger_matrix(),
            )

    def test_required_private_migration_is_exact_absent_to_present(self):
        self.assertTrue(upgrade_state.compare_private_state(
            raw_private(private=""), raw_private(private="reviewed-provenance"),
            ["litellm_agent"], "litellm_agent",
        ))
        rejected = [
            (raw_private(private=""), raw_private(private="")),
            (raw_private(private="already-present"), raw_private(private="")),
            (raw_private(private="already-present"), raw_private(private="changed")),
        ]
        for before, after in rejected:
            with self.subTest(private_after=bool(after["resources"][0]["instances"][0]["private"])), self.assertRaises(
                upgrade_state.UpgradeStateError
            ):
                upgrade_state.compare_private_state(
                    before, after, ["litellm_agent"], "litellm_agent"
                )
        with self.assertRaises(upgrade_state.UpgradeStateError):
            upgrade_state.compare_private_state(
                {"resources": [
                    *raw_private()["resources"],
                    *raw_private("litellm_team")["resources"],
                ]},
                {"resources": [
                    *raw_private(private="reviewed")["resources"],
                    *raw_private("litellm_team", "other")["resources"],
                ]},
                ["litellm_agent", "litellm_team"], "litellm_agent",
            )

    def test_exact_public_review_keeps_identity_and_computed_values_exact(self):
        before = agent_values()
        changed_identity = agent_values()
        changed_identity["id"] = "changed"
        changed_computed = agent_values()
        changed_computed["agent_name"] = "changed"
        matrix = agent_private_trigger_matrix()
        matrix["upgrade_expected_computed_migrations"] = {
            "litellm_agent": ["agent_name"]
        }
        for after in (changed_identity, changed_computed):
            with self.subTest(identity=after is changed_identity), self.assertRaises(
                upgrade_state.UpgradeStateError
            ):
                upgrade_state.compare_state_values(
                    state(resource("litellm_agent.test", "litellm_agent", before)),
                    state(resource("litellm_agent.test", "litellm_agent", after)),
                    provider_schema(), matrix, exact_public=True,
                )
        missing_identity = agent_values()
        missing_identity.pop("id")
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "not known"):
            upgrade_state.compare_state_values(
                state(resource("litellm_agent.test", "litellm_agent", missing_identity)),
                state(resource("litellm_agent.test", "litellm_agent", missing_identity)),
                provider_schema(), matrix, exact_public=True,
            )

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

    def test_reviewed_nested_computed_migration_accepts_one_set_element(self):
        before_values = members_values()
        before_values["member"] = [{
            "user_email": "one@example.invalid", "role": "user"
        }]
        after_values = members_values()
        after_values["member"] = [{
            "user_id": "generated-one", "user_email": "one@example.invalid", "role": "user"
        }]
        self.assertTrue(self.compare(
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
            self.member_migration_matrix(),
        ))

    def test_reviewed_nested_computed_migration_masks_before_set_sorting(self):
        before_values = members_values()
        for member in before_values["member"]:
            member.pop("user_id")
        after_values = members_values()
        after_values["member"][0]["user_id"] = "generated-z"
        after_values["member"][1]["user_id"] = "generated-a"
        after_values["member"].reverse()
        self.assertTrue(self.compare(
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
            self.member_migration_matrix(),
        ))

    def test_reviewed_nested_migration_does_not_hide_sibling_mutations(self):
        for sibling, replacement in (("role", "owner"), ("user_email", "changed@example.invalid")):
            before_values = members_values()
            for member in before_values["member"]:
                member.pop("user_id")
            after_values = members_values()
            after_values["member"].reverse()
            after_values["member"][0][sibling] = replacement
            with self.subTest(sibling=sibling), self.assertRaisesRegex(
                upgrade_state.UpgradeStateError, "member"
            ):
                self.compare(
                    state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
                    state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
                    self.member_migration_matrix(),
                )

    def test_reviewed_nested_migration_does_not_hide_set_cardinality(self):
        before_values = members_values()
        for member in before_values["member"]:
            member.pop("user_id")
        after_values = members_values()
        after_values["member"].pop()
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "member"):
            self.compare(
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", before_values)),
                state(resource("litellm_team_member_add.test", "litellm_team_member_add", after_values)),
                self.member_migration_matrix(),
            )

    def test_unchanged_reviewed_nested_leaf_is_harmless(self):
        values = members_values()
        self.assertFalse(self.compare(
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", values)),
            state(resource("litellm_team_member_add.test", "litellm_team_member_add", members_values())),
            self.member_migration_matrix(),
        ))

    def test_migration_paths_preserve_map_keys(self):
        before_values = agent_values()
        after_values = agent_values()
        after_values["agent_card"]["skills"][0]["routes"]["primary"]["target"] = "backend-b"
        matrix = {"upgrade_expected_computed_migrations": {
            "litellm_agent": ["agent_card.skills[*].routes[*].target"]
        }}
        self.assertTrue(self.compare(
            state(resource("litellm_agent.test", "litellm_agent", before_values)),
            state(resource("litellm_agent.test", "litellm_agent", after_values)),
            matrix,
        ))
        renamed = agent_values()
        renamed["agent_card"]["skills"][0]["routes"]["secondary"] = \
            renamed["agent_card"]["skills"][0]["routes"].pop("primary")
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "agent_card"):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", before_values)),
                state(resource("litellm_agent.test", "litellm_agent", renamed)),
                matrix,
            )

    def test_migration_path_grammar_and_schema_validation_fail_closed(self):
        cases = {
            "member..user_id": "malformed",
            "member[0].user_id": "malformed",
            "member[*].missing": "absent",
            "member.user_id": "must use",
            "member[*].role": "not computed",
            "member[*]": "whole structure",
        }
        before = state(resource(
            "litellm_team_member_add.test", "litellm_team_member_add", members_values()
        ))
        for path, message in cases.items():
            with self.subTest(path=path), self.assertRaisesRegex(
                upgrade_state.UpgradeStateError, message
            ):
                self.compare(before, before, self.member_migration_matrix(path))

        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "non-collection"):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", agent_values())),
                state(resource("litellm_agent.test", "litellm_agent", agent_values())),
                {"upgrade_expected_computed_migrations": {
                    "litellm_agent": ["agent_card[*].name"]
                }},
            )

    def test_migration_paths_reject_sensitive_duplicate_and_overlapping_paths(self):
        schema = provider_schema()
        member_attributes = schema["resource_schemas"]["litellm_team_member_add"]["block"]["block_types"]["member"]["block"]["attributes"]
        member_attributes["secret"] = attribute("string", sensitive=True, computed=True)
        before = state(resource(
            "litellm_team_member_add.test", "litellm_team_member_add", members_values()
        ))
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "sensitive"):
            self.compare(before, before, self.member_migration_matrix("member[*].secret"), schema)
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "duplicate"):
            self.compare(before, before, self.member_migration_matrix(
                "member[*].user_id", "member[*].user_id"
            ), schema)
        with self.assertRaisesRegex(upgrade_state.UpgradeStateError, "overlapping"):
            self.compare(before, before, self.member_migration_matrix(
                "member[*]", "member[*].user_id"
            ), schema)

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

    def test_exact_comparison_distinguishes_absent_null_and_empty_representations(self):
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
        with self.assertRaises(upgrade_state.UpgradeStateError):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", absent)),
                state(resource("litellm_agent.test", "litellm_agent", explicit)),
                exact_public=True,
            )
        canonical = upgrade_state.canonicalize_resources(
            state(resource("litellm_agent.test", "litellm_agent", absent)), provider_schema()
        )["litellm_agent.test"]["values"]
        self.assertIsInstance(canonical["profile"], upgrade_state.TypedAbsence)
        self.assertIn("missing", canonical["profile"].shape)
        self.assertIsInstance(
            canonical["agent_card"]["skills"][0]["aliases"], upgrade_state.TypedAbsence
        )
        self.assertIn("missing", canonical["agent_card"]["skills"][0]["aliases"].shape)

    def test_agent_old_state_representation_migrations_are_exact_and_directional(self):
        before = agent_values()
        after = agent_values()
        after["agent_card"]["signatures"] = []
        after["agent_card"]["supports_authenticated_extended_card"] = None
        matrix = {"upgrade_expected_representation_migrations": {
            "litellm_agent": {
                "agent_card.signatures": "missing-to-empty-list-block",
                "agent_card.supports_authenticated_extended_card": "missing-to-null-bool",
            }
        }}
        self.assertTrue(self.compare(
            state(resource("litellm_agent.test", "litellm_agent", before)),
            state(resource("litellm_agent.test", "litellm_agent", after)),
            matrix, exact_public=True,
        ))
        for mutate in (
            lambda values: values["agent_card"].update({"signatures": [{"protected": "x", "signature": "y"}]}),
            lambda values: values["agent_card"].update({"supports_authenticated_extended_card": False}),
            lambda values: values["agent_card"].update({"name": "changed"}),
        ):
            changed = agent_values()
            mutate(changed)
            with self.assertRaises(upgrade_state.UpgradeStateError):
                self.compare(
                    state(resource("litellm_agent.test", "litellm_agent", before)),
                    state(resource("litellm_agent.test", "litellm_agent", changed)),
                    matrix, exact_public=True,
                )
        with self.assertRaises(upgrade_state.UpgradeStateError):
            self.compare(
                state(resource("litellm_agent.test", "litellm_agent", after)),
                state(resource("litellm_agent.test", "litellm_agent", before)),
                matrix, exact_public=True,
            )

    def test_representation_migration_contract_rejects_wrong_schema_shapes(self):
        bad_rules = (
            {"agent_card.name": "missing-to-null-bool"},
            {"agent_card": "missing-to-empty-list-block"},
            {"agent_card.signatures[*].protected": "missing-to-null-bool"},
            {"agent_card.missing": "missing-to-null-bool"},
            {"agent_card.signatures": "unsupported"},
        )
        for rules in bad_rules:
            with self.subTest(rules=rules), self.assertRaises(upgrade_state.UpgradeStateError):
                upgrade_state.compile_upgrade_contract(
                    provider_schema(),
                    {"upgrade_expected_representation_migrations": {"litellm_agent": rules}},
                )

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
