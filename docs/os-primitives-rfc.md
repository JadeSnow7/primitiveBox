Unix 资源            Primitive 对象化              CVR 语义
─────────────────────────────────────────────────────────────
文件/目录            fs.*                          已有 ✓
                     fs.read / fs.write / fs.diff
                     checkpoint_required: true on write

进程                 process.spawn                 checkpoint before spawn
                     process.signal                risk: high, reversible: false
                     process.wait                  verify: exit code check
                     process.list                  read-only

网络                 net.listen                    risk: medium
                     net.connect                   timeout-bounded
                     net.dns.resolve               read-only
                     net.firewall.allow            risk: high, requires escalation

系统服务             service.start                 checkpoint before start
                     service.stop                  reversible: true (can restart)
                     service.status                read-only
                     service.logs                  read-only, streaming

包管理               pkg.install                   checkpoint_required: true
                     pkg.remove                    risk: high
                     pkg.list                      read-only
                     pkg.verify                    verify strategy: checksum

用户/权限            user.create                   risk: high, escalation
                     user.chmod                    reversible: true
                     user.chown                    reversible: true

容器/namespace       container.create              sandbox-scoped
                     container.exec                delegates to inner runtime
                     cgroup.limit                  resource boundary primitive

设备                 device.mount                  risk: high
                     device.unmount                reversible: true
                     device.list                   read-only
