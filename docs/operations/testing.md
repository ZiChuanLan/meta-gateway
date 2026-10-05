# 端到端验证

容器 E2E 用显式测试密钥跑，不碰你的开发数据：

```bash
ADMIN_TOKEN=ci-admin-token \
MASTER_KEY=ci-master-key-at-least-32-characters \
METRICS_TOKEN=ci-metrics-token \
docker compose -f docker-compose.yml -f docker-compose.e2e.yml \
  up --build --abort-on-container-exit --exit-code-from e2e e2e
```

CI 还会重启网关，并对持久化卷跑一次 `e2e-runner verify`，验证「重启后数据还在」。

## 出网例外

E2E 的 mock 上游主机名是**唯一**允许的私网目标；另一个 loopback 目标必须保持被拒。这条约束同时验证了出网策略确实在拦私网地址，而不是配置写了没生效。

## 相关

- [部署与反代](./deployment)
- [出网策略](/upstream/#出网策略)
