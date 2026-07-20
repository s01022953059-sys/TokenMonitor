import threading
import unittest
from unittest import mock

import server


class ServerCommunityAutoReportTests(unittest.TestCase):
    def test_report_runs_without_opening_community_page(self):
        stop_event = threading.Event()
        usage = {"cache_state": "ready", "summary": {"total_tokens": 123}}

        def report_once(value):
            self.assertIs(value, usage)
            stop_event.set()

        with mock.patch.object(server, "get_cached_usage", return_value=usage), \
             mock.patch.object(server, "report_community_stats", side_effect=report_once) as report:
            server._community_report_loop(stop_event=stop_event, initial_delay=0, interval=0)

        report.assert_called_once_with(usage)

    def test_cold_start_waits_for_usage_snapshot_before_reporting(self):
        stop_event = threading.Event()
        warming = {"cache_state": "warming"}
        ready = {"cache_state": "ready"}
        values = iter([warming, ready])

        def report_once(_value):
            stop_event.set()

        with mock.patch.object(server, "get_cached_usage", side_effect=lambda: next(values)), \
             mock.patch.object(server, "report_community_stats", side_effect=report_once) as report:
            server._community_report_loop(stop_event=stop_event, initial_delay=0, interval=0)

        report.assert_called_once_with(ready)


if __name__ == "__main__":
    unittest.main()
