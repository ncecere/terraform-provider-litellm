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


if __name__ == "__main__":
    unittest.main()
