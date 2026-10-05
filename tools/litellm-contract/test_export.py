#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from fastapi import APIRouter, FastAPI

sys.dont_write_bytecode = True
MODULE_PATH = Path(__file__).with_name("export.py")
SPEC = importlib.util.spec_from_file_location("litellm_contract_export", MODULE_PATH)
assert SPEC and SPEC.loader
exporter = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = exporter
SPEC.loader.exec_module(exporter)


def include_router(app, module):
    app.include_router(module.router)


def fake_feature(name="reviewed", module="fake.reviewed", register=include_router):
    return SimpleNamespace(
        name=name,
        module_path=module,
        path_prefixes=("/reviewed",),
        path_suffixes=(),
        register_fn=register,
        persistent_swagger_stub=False,
    )


REVIEWED = exporter.feature("reviewed", "fake.reviewed", ("/reviewed",))


class LazyExporterAdversarialTests(unittest.TestCase):
    def test_missing_feature_fails(self):
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (REVIEWED,)):
            with self.assertRaisesRegex(RuntimeError, "definitions differ"):
                exporter.validate_feature_definitions([])

    def test_stale_feature_contract_fails(self):
        stale = fake_feature(module="fake.stale")
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (REVIEWED,)):
            with self.assertRaisesRegex(RuntimeError, "definitions differ"):
                exporter.validate_feature_definitions([stale])

    def test_duplicate_feature_fails(self):
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (REVIEWED, REVIEWED)):
            with self.assertRaisesRegex(RuntimeError, "duplicate lazy feature"):
                exporter.validate_feature_definitions([fake_feature(), fake_feature()])

    def test_broken_import_fails(self):
        app = FastAPI()
        item = fake_feature()
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (REVIEWED,)):
            with self.assertRaisesRegex(RuntimeError, "failed to import"):
                exporter.direct_register_features(app, [item], importer=lambda _: (_ for _ in ()).throw(ImportError("broken")))

    def test_broken_registration_fails(self):
        def broken(app, module):
            raise ValueError("broken")

        app = FastAPI()
        item = fake_feature(register=broken)
        expected = exporter.feature("reviewed", "fake.reviewed", ("/reviewed",))
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (expected,)):
            with self.assertRaisesRegex(RuntimeError, "failed to register"):
                exporter.direct_register_features(app, [item], importer=lambda _: SimpleNamespace(router=APIRouter()))

    def test_zero_route_feature_fails(self):
        app = FastAPI()
        item = fake_feature()
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (REVIEWED,)):
            with self.assertRaisesRegex(RuntimeError, r"zero (?:live HTTP )?routes"):
                exporter.direct_register_features(app, [item], importer=lambda _: SimpleNamespace(router=APIRouter()))

    def test_unextractable_mounted_application_fails(self):
        prefix, attr_name = "/reviewed", "app"

        def mount(app, module):
            app.mount(path=prefix, app=getattr(module, attr_name))

        item = fake_feature(register=mount)
        reviewed = exporter.feature(
            "reviewed", "fake.reviewed", ("/reviewed",),
            registration="mount_app", attribute="app", mount_prefix="/reviewed",
        )
        with mock.patch.object(exporter, "EXPECTED_LAZY_FEATURES", (reviewed,)):
            with self.assertRaisesRegex(RuntimeError, "mounted application extraction failed"):
                exporter.direct_register_features(FastAPI(), [item], importer=lambda _: SimpleNamespace(app=SimpleNamespace()))


class OpenAPIVisibilityTests(unittest.TestCase):
    def test_plain_starlette_routes_are_not_openapi_operations(self):
        from starlette.routing import Route

        app = FastAPI()

        @app.get("/documented")
        def documented():
            return {}

        app.router.routes.append(Route("/transport", endpoint=lambda request: None, methods=["GET"]))
        visible = exporter.route_operations(app.routes, include_hidden=False)
        live = exporter.route_operations(app.routes)
        self.assertIn(("GET", "/documented"), visible)
        self.assertNotIn(("GET", "/transport"), visible)
        self.assertIn(("GET", "/transport"), live)


class OrganizationUpdateRouteTests(unittest.TestCase):
    def write_source(self, root, decorator, parameters="organization_id: str"):
        source = root / exporter.ORGANIZATION_UPDATE_ROUTE_SOURCE
        source.parent.mkdir(parents=True)
        source.write_text(
            f"@router.patch(\"/v2/organization/{{organization_id}}\"{decorator})\n"
            f"async def update_organization({parameters}):\n    pass\n",
            encoding="utf-8",
        )

    def run_check(self, decorator, parameters="organization_id: str"):
        import tempfile

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_source(root, decorator, parameters)
            exporter.verify_public_organization_update_route(root)

    def test_public_route_passes(self):
        self.run_check(", tags=[\"organization management\"]")

    def test_explicitly_public_route_passes(self):
        self.run_check(", include_in_schema=True")

    def test_hidden_route_fails(self):
        with self.assertRaisesRegex(RuntimeError, "hidden from OpenAPI"):
            self.run_check(", include_in_schema=False")

    def test_changed_path_parameter_fails(self):
        with self.assertRaisesRegex(RuntimeError, "path parameter changed"):
            self.run_check("", parameters="org_id: str")


if __name__ == "__main__":
    unittest.main()
