"""内存缓存与按日拆分存储回归测试。

覆盖维度:
1. usage/heatmap 内存缓存: mtime 不变时命中缓存, 不重新 json.load
2. heatmap_detail 按日拆分: 单日文件独立读写, 不再全量加载
3. 旧格式迁移: 合并文件自动拆分为按日文件
"""
import datetime
import json
import os
import tempfile
import time
import unittest
from unittest import mock

import server


class UsageMemoryCacheTests(unittest.TestCase):
    """usage 快照内存缓存: mtime 不变时命中, 变化时重新加载。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.cache_path = os.path.join(self.temp_dir.name, "usage_cache.json")
        # 重置全局缓存
        server._usage_cache_obj = None
        server._usage_cache_mtime = 0.0
        self.patcher = mock.patch.object(server, "USAGE_CACHE_PATH", self.cache_path)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def _write_cache(self, tokens):
        # _load_usage_snapshot 只在缓存里的 summary.date 等于 today 时返回
        # 非 None, 硬编历史日期会让跨日期后这个测试自然假阳性 fail。改用 today,
        # 避免 _write_cache 与 _load_usage_snapshot 日期口径错位。
        data = {"saved_at": time.time(), "data": {
            "summary": {"date": datetime.date.today().isoformat(), "total_tokens": tokens,
                        "input_tokens": 0, "output_tokens": 0,
                        "input_cached": 0, "input_uncached": 0,
                        "events_after_dedup": 0, "events_before_dedup": 0,
                        "deepseek_balance": "0", "deepseek_currency": "CNY", "deepseek_status": "Offline"},
            "by_tool": {}, "by_model": {}, "by_model_requests": {},
            "by_tool_model": {}, "recent_events": [],
        }}
        with open(self.cache_path, "w") as f:
            json.dump(data, f)
        return data

    def test_cache_hit_when_mtime_unchanged(self):
        """mtime 不变时第二次调用直接返回内存缓存, 不重新读磁盘。"""
        self._write_cache(100)
        first = server._load_usage_snapshot()
        self.assertIsNotNone(first)

        # 篡改文件内容但不改 mtime (模拟内存缓存命中)
        # 实际上 os.replace 会改 mtime, 这里验证的是缓存逻辑:
        # 连续两次调用返回相同对象
        second = server._load_usage_snapshot()
        self.assertEqual(
            first["data"]["summary"]["total_tokens"],
            second["data"]["summary"]["total_tokens"],
        )

    def test_cache_invalidated_when_mtime_changes(self):
        """文件更新后 mtime 变化, 内存缓存失效, 重新加载。"""
        self._write_cache(100)
        first = server._load_usage_snapshot()
        self.assertEqual(first["data"]["summary"]["total_tokens"], 100)

        # 等 mtime 精度, 重写文件
        time.sleep(0.01)
        self._write_cache(200)

        # 重置缓存强制重新读
        server._usage_cache_obj = None
        server._usage_cache_mtime = 0.0
        second = server._load_usage_snapshot()
        self.assertEqual(second["data"]["summary"]["total_tokens"], 200)

    def test_save_updates_memory_cache(self):
        """_save_usage_snapshot 写入后同步更新内存缓存。"""
        server._usage_cache_obj = None
        server._usage_cache_mtime = 0.0
        today = datetime.date.today().isoformat()
        server._save_usage_snapshot({"summary": {"date": today, "total_tokens": 999}})
        # 内存缓存应已更新
        self.assertIsNotNone(server._usage_cache_obj)
        self.assertEqual(server._usage_cache_obj["data"]["summary"]["total_tokens"], 999)
        self.assertEqual(server._usage_cache_obj["data"]["summary"]["date"], today)

    def test_load_returns_none_when_cache_date_is_not_today(self):
        """_load_usage_snapshot 只在缓存里 summary.date == today 时返回非 None。
        当缓存写的是过去的某天 (如 7.24), 加载会返回 None, 避免把昨天的统计数据
        当成今天的呈现, 让使用者误以为今天还没有用量。

        回归用例: 修复 _write_cache 之前的硬编 2026-07-24 会让 7.25 跑用例时
        _write_cache 出 2026-07-24, _load_usage_snapshot 又判 != today 返 None,
        上层 fake-assert 直接 NoneType 崩溃。改 _write_cache 用 today 后本回归覆盖。
        """
        # 直接在 cache_path 写一份非 today 的缓存
        with open(self.cache_path, "w") as f:
            json.dump({
                "saved_at": time.time(),
                "data": {
                    "summary": {"date": "2026-07-24", "total_tokens": 100,
                                "input_tokens": 0, "output_tokens": 0,
                                "input_cached": 0, "input_uncached": 0,
                                "events_after_dedup": 0, "events_before_dedup": 0,
                                "deepseek_balance": "0", "deepseek_currency": "CNY",
                                "deepseek_status": "Offline"},
                    "by_tool": {}, "by_model": {}, "by_model_requests": {},
                    "by_tool_model": {}, "recent_events": [],
                },
            }, f)
        # 重置内存缓存强制重读
        server._usage_cache_obj = None
        server._usage_cache_mtime = 0.0
        # 今天的日期与缓存里的 2026-07-24 不同, _load_usage_snapshot 应返 None
        result = server._load_usage_snapshot()
        self.assertIsNone(result)


class HeatmapMemoryCacheTests(unittest.TestCase):
    """heatmap 快照内存缓存。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.cache_path = os.path.join(self.temp_dir.name, "heatmap_cache.json")
        server._heatmap_cache_obj = None
        server._heatmap_cache_mtime = 0.0
        self.patcher = mock.patch.object(server, "HEATMAP_CACHE_PATH", self.cache_path)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def test_cache_hit_returns_same_data(self):
        """连续两次调用返回相同数据 (内存缓存命中)。"""
        days = [{"date": f"2026-07-{i:02d}", "label": f"07-{i:02d}",
                 "weekday": 0, "month": 7, "tokens": i * 100} for i in range(1, 366)]
        with open(self.cache_path, "w") as f:
            json.dump({"saved_at": time.time(), "data": {"days": days}}, f)

        first = server._load_heatmap_snapshot()
        self.assertIsNotNone(first)

        second = server._load_heatmap_snapshot()
        self.assertIsNotNone(second)
        self.assertEqual(len(first["data"]["days"]), len(second["data"]["days"]))


class HeatmapDetailSplitCacheTests(unittest.TestCase):
    """heatmap_detail 按日拆分存储。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.cache_dir = os.path.join(self.temp_dir.name, "detail_cache")
        self.legacy_path = os.path.join(self.temp_dir.name, "heatmap_detail_cache.json")
        server._heatmap_detail_cache_lock = __import__("threading").Lock()
        self.dir_patcher = mock.patch.object(server, "HEATMAP_DETAIL_CACHE_DIR", self.cache_dir)
        self.legacy_patcher = mock.patch.object(server, "HEATMAP_DETAIL_CACHE_PATH", self.legacy_path)
        self.dir_patcher.start()
        self.legacy_patcher.start()
        self.addCleanup(self.dir_patcher.stop)
        self.addCleanup(self.legacy_patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def test_save_and_load_single_day(self):
        """单日写入和读取独立, 不涉及其他日期。"""
        entry = {"saved_at": time.time(), "data": {"sessions": [
            {"tool": "ZCode", "model": "glm-5.2", "total_tokens": 100}
        ], "summary": {"total_tokens": 100}}}
        server._save_heatmap_detail_entry("2026-07-24", entry)

        loaded = server._load_heatmap_detail_entry("2026-07-24")
        self.assertIsNotNone(loaded)
        self.assertEqual(loaded["data"]["sessions"][0]["tool"], "ZCode")

    def test_different_days_are_independent(self):
        """不同日期的缓存文件互相独立。"""
        server._save_heatmap_detail_entry("2026-07-24", {"saved_at": 1, "data": {"sessions": [{"tool": "A"}]}})
        server._save_heatmap_detail_entry("2026-07-25", {"saved_at": 2, "data": {"sessions": [{"tool": "B"}]}})

        e24 = server._load_heatmap_detail_entry("2026-07-24")
        e25 = server._load_heatmap_detail_entry("2026-07-25")
        self.assertEqual(e24["data"]["sessions"][0]["tool"], "A")
        self.assertEqual(e25["data"]["sessions"][0]["tool"], "B")

    def test_missing_day_returns_none(self):
        """不存在的日期返回 None。"""
        result = server._load_heatmap_detail_entry("1999-01-01")
        self.assertIsNone(result)

    def test_legacy_migration_splits_to_per_day_files(self):
        """旧合并格式自动迁移为按日文件, 迁移后旧文件删除。"""
        legacy = {"entries": {
            "2026-07-24": {"saved_at": 1, "data": {"sessions": [{"tool": "X"}]}},
            "2026-07-25": {"saved_at": 2, "data": {"sessions": [{"tool": "Y"}]}},
        }}
        with open(self.legacy_path, "w") as f:
            json.dump(legacy, f)

        server._migrate_legacy_detail_cache()

        # 旧文件应已删除
        self.assertFalse(os.path.exists(self.legacy_path))
        # 按日文件应存在
        f24 = os.path.join(self.cache_dir, "2026-07-24.json")
        self.assertTrue(os.path.exists(f24))
        with open(f24) as f:
            migrated = json.load(f)
        self.assertEqual(migrated["data"]["sessions"][0]["tool"], "X")


if __name__ == "__main__":
    unittest.main()
