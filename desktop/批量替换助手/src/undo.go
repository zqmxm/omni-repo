
package main

// undo.go —— 撤销清单：集中管理备份、支持多步撤销（纯标准库，跨平台）

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	manifestVersion = 2
	maxUndoOps      = 10 // 最多保留最近 10 次操作，超出的备份自动清理
)

// UndoEntry 一次替换中的一个文件动作
type UndoEntry struct {
	Kind      string `json:"kind"`                // inplace | saveas | rename
	Original  string `json:"original"`            // 源文件绝对路径
	Produced  string `json:"produced"`            // 产出路径（原地时等于 Original）
	Backup    string `json:"backup,omitempty"`    // 原地替换前的字节级备份
	RenamedTo string `json:"renamedTo,omitempty"` // 改名后的文件名
}

// UndoOp 一次「开始替换」的完整动作集合
type UndoOp struct {
	Time    string      `json:"time"`
	Dir     string      `json:"dir"`
	SaveAs  string      `json:"saveAs,omitempty"`
	Entries []UndoEntry `json:"entries"`
}

// UndoManifest 撤销栈，写到单个 JSON 文件
type UndoManifest struct {
	Version int      `json:"version"`
	Ops     []UndoOp `json:"ops"`
}

// UndoResult 单个动作的撤销结果
type UndoResult struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func isWritableDir(d string) bool {
	f, err := os.CreateTemp(d, ".nf-w-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// defaultStateDir 优先用 exe 同目录；不可写（如 Program Files）时回退用户配置目录、临时目录
func defaultStateDir() string {
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		if isWritableDir(d) {
			return d
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		d := filepath.Join(cfg, "batchreplace")
		if os.MkdirAll(d, 0755) == nil && isWritableDir(d) {
			return d
		}
	}
	d := filepath.Join(os.TempDir(), "batchreplace")
	_ = os.MkdirAll(d, 0755)
	return d
}

// StateFilePath 返回撤销清单路径；explicit 非空时直接用作清单路径
func StateFilePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return filepath.Join(defaultStateDir(), "batchreplace-undo.json")
}

// backupDirFor 备份与清单放在同一目录下，便于用 --state 完整隔离
func backupDirFor(manifestPath string) string {
	return filepath.Join(filepath.Dir(manifestPath), "batchreplace-backups")
}

func loadManifest(path string) (*UndoManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取撤销记录失败：%w", err)
	}
	m := &UndoManifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("撤销记录格式损坏：%w", err)
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("撤销记录版本不兼容（%d），无法撤销", m.Version)
	}
	return m, nil
}

func saveManifest(path string, m *UndoManifest) error {
	m.Version = manifestVersion
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nf-undo-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// appendUndoOp 追加一次操作到撤销栈；超出上限时清理最老操作的备份
func appendUndoOp(path string, op UndoOp) error {
	m := &UndoManifest{}
	if old, err := loadManifest(path); err == nil {
		m = old
	}
	op.Time = time.Now().Format("2006-01-02 15:04:05")
	m.Ops = append(m.Ops, op)
	for len(m.Ops) > maxUndoOps {
		dropBackupFiles(m.Ops[0])
		m.Ops = m.Ops[1:]
	}
	return saveManifest(path, m)
}

func dropBackupFiles(op UndoOp) {
	for _, e := range op.Entries {
		if e.Backup != "" {
			_ = os.Remove(e.Backup)
		}
	}
}

// makeBackup 把原文件字节集中备份到 backups 目录，返回备份文件路径
func makeBackup(manifestPath, original string) (string, error) {
	dir := backupDirFor(manifestPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "bak-*.bin")
	if err != nil {
		return "", err
	}
	dst := f.Name()
	f.Close()
	if err := copyFile(original, dst); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// copyFile 复制文件并保留权限位
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if fi, e := os.Stat(src); e == nil {
		_ = os.Chmod(dst, fi.Mode())
	}
	return nil
}

// HasUndo 是否还有可撤销的操作
func HasUndo(statePath string) bool {
	m, err := loadManifest(StateFilePath(statePath))
	return err == nil && len(m.Ops) > 0
}

// UndoLast 撤销最近一次「开始替换」，支持连续多次调用
func UndoLast(statePath string) ([]UndoResult, string, error) {
	path := StateFilePath(statePath)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return nil, "", fmt.Errorf("没有可撤销的操作")
	}
	m, err := loadManifest(path)
	if err != nil {
		return nil, "", err
	}
	if len(m.Ops) == 0 {
		return nil, "", fmt.Errorf("没有可撤销的操作")
	}
	op := m.Ops[len(m.Ops)-1]
	m.Ops = m.Ops[:len(m.Ops)-1]

	var results []UndoResult
	for i := len(op.Entries) - 1; i >= 0; i-- {
		e := op.Entries[i]
		r := UndoResult{Path: e.Original, Action: e.Kind}
		switch e.Kind {
		case "rename":
			src := e.Produced
			if _, err := os.Stat(src); err != nil {
				r.Message = "改名后的文件已不存在，跳过"
			} else if _, err := os.Stat(e.Original); err == nil {
				r.Message = "原路径已被占用，跳过"
			} else if err := os.Rename(src, e.Original); err != nil {
				r.Message = "恢复文件名失败：" + err.Error()
			} else {
				r.OK = true
				r.Message = "已恢复原文件名 " + filepath.Base(e.Original)
			}
		case "saveas":
			if err := os.Remove(e.Produced); err != nil && !os.IsNotExist(err) {
				r.Message = "删除另存文件失败：" + err.Error()
			} else {
				r.OK = true
				r.Message = "已删除另存文件 " + filepath.Base(e.Produced)
				removeEmptyParents(filepath.Dir(e.Produced), op.SaveAs)
			}
			r.Path = e.Produced
		case "inplace":
			if e.Backup == "" {
				r.Message = "缺少备份，无法还原"
			} else if err := copyFile(e.Backup, e.Original); err != nil {
				r.Message = "还原失败：" + err.Error()
			} else {
				_ = os.Remove(e.Backup)
				r.OK = true
				r.Message = "已还原原文件 " + filepath.Base(e.Original)
			}
		default:
			r.Message = "未知动作类型"
		}
		results = append(results, r)
	}

	if len(m.Ops) == 0 {
		_ = os.Remove(path)
	} else if err := saveManifest(path, m); err != nil {
		return results, op.Time, fmt.Errorf("撤销已完成，但更新撤销记录失败：%w", err)
	}
	return results, op.Time, nil
}

// removeEmptyParents 删除另存产生的空目录，直到 stopAt 为止（遇到非空或到达即停）
func removeEmptyParents(dir, stopAt string) {
	for i := 0; i < 32; i++ {
		if stopAt != "" && samePath(dir, stopAt) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return // 非空或无权限，正常结束
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
