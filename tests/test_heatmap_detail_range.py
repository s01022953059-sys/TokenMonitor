import datetime as dt
import tempfile
import unittest
from unittest import mock

import scanner


class HeatmapDetailRangeTests(unittest.TestCase):
    def test_date_detail_scans_only_the_requested_day(self):
        target = dt.date(2026, 7, 13)
        start = int(dt.datetime.combine(target, dt.time.min).timestamp())
        end = int((dt.datetime.combine(target, dt.time.min) + dt.timedelta(days=1)).timestamp())

        with tempfile.TemporaryDirectory() as root, mock.patch.multiple(
            scanner,
            CC_SWITCH_DB_PATH=root + "/missing-cc.db",
            HERMES_DB_PATH=root + "/missing-hermes.db",
            WORKBUDDY_DB_PATH=root + "/missing-workbuddy.db",
            WORKBUDDY_PROJECTS_DIR=root + "/missing-projects",
        ), mock.patch.object(scanner, "scan_codex_tokens", return_value=[]) as codex:
            scanner.get_heatmap_detail(date=target.isoformat(), page=1, page_size=50)

        codex.assert_called_once_with(start, end)
