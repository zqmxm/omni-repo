
package main

// engine.go —— 执行引擎：扫描、内容替换、文件名替换、另存、撤销记录
// 纯标准库逻辑，Windows / Linux / macOS 通用。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxFileSize 单个文档上限 100MB，防止异常文件
const maxFileSize = 100 * 1024 * 1024

type fileJob struct {
	src         string
	out         string
	kind        string
	nameChanged bool
}

type jobResult struct {
	item  Item
	entry *UndoEntry
}

// ExecStats 是一次执行的汇总统计。
type ExecStats struct {
	Done    int
	Failed  int
	Skipped int
}

// Execute 按请求执行全部替换，通过 onItem 回调逐项汇报进度。
// 签名保持不变，GUI / CLI / 既有测试共用。
func Execute(req ReplaceReq, onItem func(Item)) (done, failed int) {
	s := ExecuteEx(req, onItem)
	return s.Done, s.Failed
}

// ExecuteEx 与 Execute 行为一致，额外返回「跳过」数量，供界面显示准确统计。
func ExecuteEx(req ReplaceReq, onItem func(Item)) ExecStats {
	var done, failed, skipped int
	if err := validateReq(&req); err != nil {
		onItem(Item{Path: req.Dir, Kind: "error", Status: "failed", Message: err.Error()})
		return ExecStats{Failed: 1}
	}
	files, err := scanFiles(req)
	if err != nil {
		onItem(Item{Path: req.Dir, Kind: "error", Status: "failed", Message: err.Error()})
		return ExecStats{Failed: 1}
	}

	manifestPath := StateFilePath(req.StatePath)
	if req.SaveAs != "" && !req.DryRun {
		_ = os.MkdirAll(req.SaveAs, 0755)
	}

	jobs := make([]fileJob, 0, len(files))
	for _, path := range files {
		out, changed, e := outputPathFor(req, path)
		if e != nil {
			onItem(Item{Path: path, Kind: "error", Status: "failed", Message: e.Error()})
			failed++
			continue
		}
		jobs = append(jobs, fileJob{src: path, out: out, kind: kindOfFile(path), nameChanged: changed})
	}

	// v3.1.6：给本次操作一个唯一 ID，供撤销栈按 ID 增量落盘
	op := UndoOp{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Dir: req.Dir, SaveAs: req.SaveAs}
	var opMu sync.Mutex
	addEntry := func(e *UndoEntry) {
		if e == nil {
			return
		}
		opMu.Lock()
		op.Entries = append(op.Entries, *e)
		opMu.Unlock()
	}
	report := func(r jobResult) {
		onItem(r.item)
		addEntry(r.entry)
		switch r.item.Status {
		case "ok":
			done++
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
		// v3.1.6：每完成一个文件就把撤销进度落盘一次；失败不刷屏，末尾会兜底
		if !req.DryRun && r.entry != nil {
			opMu.Lock()
			snap := op
			snap.Entries = append([]UndoEntry(nil), op.Entries...)
			opMu.Unlock()
			_ = upsertUndoOp(manifestPath, snap)
		}
	}

	// ① 文档内容替换（并发）
	if req.ReplaceContent {
		workers := runtime.NumCPU()
		if workers > 8 {
			workers = 8
		}
		if workers < 1 {
			workers = 1
		}
		jobCh := make(chan fileJob, len(jobs))
		resCh := make(chan jobResult, len(jobs))
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					resCh <- processJob(j, req, manifestPath)
				}
			}()
		}
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
		wg.Wait()
		close(resCh)
		for r := range resCh {
			report(r)
		}
	} else if req.RenameFiles && req.SaveAs != "" {
		// ② 只替换文件名 + 另存：把文件原样复制到新名字
		for _, j := range jobs {
			report(copyOnlyJob(j, req))
		}
	}

	// ③ 原地模式下的文件名替换（按路径深度倒序，避免父子目录改名导致路径失效）
	if req.RenameFiles && req.SaveAs == "" {
		sort.Slice(files, func(i, k int) bool {
			return strings.Count(files[i], string(os.PathSeparator)) > strings.Count(files[k], string(os.PathSeparator))
		})
		for _, path := range files {
			r := renameJob(path, req)
			if r.item.Status == "skipped" && r.item.Message == "" {
				continue // 文件名无需改动，不刷屏
			}
			report(r)
		}
	}

	// ④ 写入撤销栈（按 ID 覆盖，保证 op 完整；中途崩溃也有部分记录）
	if !req.DryRun && len(op.Entries) > 0 {
		if err := upsertUndoOp(manifestPath, op); err != nil {
			onItem(Item{Kind: "undo", Status: "skipped", Message: "替换已完成，但撤销记录写入失败：" + err.Error()})
		} else {
			onItem(Item{Kind: "undo", Status: "ok", Message: "本次操作已可撤销"})
		}
	}
	return ExecStats{Done: done, Failed: failed, Skipped: skipped}
}

// outputPathFor 计算产出路径。原地模式返回源路径；另存模式保持相对目录结构并应用文件名规则。
func outputPathFor(req ReplaceReq, src string) (string, bool, error) {
	if req.SaveAs == "" {
		return src, false, nil
	}
	rel, err := filepath.Rel(req.Dir, src)
	if err != nil {
		return "", false, err
	}
	base := filepath.Base(rel)
	name := base
	if req.RenameFiles {
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		if ns, hits := applyPairs(stem, req.Pairs); hits > 0 {
			name = ns + ext
		}
	}
	sub := filepath.Dir(rel)
	if sub == "." {
		sub = ""
	}
	return filepath.Join(req.SaveAs, sub, name), name != base, nil
}

// tempPathFor 在目标目录申请一个唯一的、当前不存在的路径，保证写入是原子的 rename
func tempPathFor(out string) (string, error) {
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".nf-tmp-*")
	if err != nil {
		return "", err
	}
	p := f.Name()
	f.Close()
	_ = os.Remove(p)
	return p, nil
}

// transformToTemp 把 src 转换后写入 tmp（不碰 src / out）
func transformToTemp(src, tmp, kind string, pairs []Pair) (int, error) {
	switch kind {
	case "docx":
		return ReplaceDocx(src, tmp, pairs)
	case "xlsx":
		return ReplaceXlsx(src, tmp, pairs)
	case "legacy-doc":
		if err := copyFile(src, tmp); err != nil {
			return 0, err
		}
		return replaceDocText(tmp, pairs)
	case "legacy-xls":
		if err := copyFile(src, tmp); err != nil {
			return 0, err
		}
		return ReplaceXls(tmp, pairs)
	}
	return 0, fmt.Errorf("不支持的文件格式")
}

// noMatchMessage 在内容无命中时给出可操作的提示。
// 典型场景：用户只想改文件名，但没勾「同时替换文件名」——
// 这时日志不该只说「没有找到匹配内容」，而要明确指出该勾哪个选项。
func noMatchMessage(src string, req ReplaceReq, base string) string {
	if !req.RenameFiles {
		name := filepath.Base(src)
		for _, p := range req.Pairs {
			if p.From != "" && strings.Contains(name, p.From) {
				return base + "：内容中没有「" + p.From + "」，但文件名里有；要改文件名请勾选「同时替换文件名」"
			}
		}
	}
	return base
}

// processJob 处理单个文件的内容替换 + 就位 + 记录撤销动作
func processJob(j fileJob, req ReplaceReq, manifestPath string) jobResult {
	item := Item{Path: j.src, Kind: j.kind}
	fail := func(msg string) jobResult {
		item.Status, item.Message = "failed", msg
		return jobResult{item: item}
	}

	// 预览模式下把临时文件放在源目录，避免「只预览」却在另存目录留下空文件夹
	tmpBase := j.out
	if req.DryRun {
		tmpBase = j.src
	}
	tmp, err := tempPathFor(tmpBase)
	if err != nil {
		return fail("创建临时文件失败：" + err.Error())
	}
	defer os.Remove(tmp)

	hits, err := transformToTemp(j.src, tmp, j.kind, req.Pairs)
	if err != nil {
		return fail("处理失败：" + err.Error())
	}
	item.Hits = hits

	if req.DryRun {
		item.Status = "skipped"
		if hits == 0 && !j.nameChanged {
			item.Message = "预览：无改动"
		} else {
			item.Message = fmt.Sprintf("预览：将替换 %d 处 → %s", hits, j.out)
			item.Output = j.out
		}
		return jobResult{item: item}
	}

	if req.SaveAs == "" {
		// 原地替换：先集中备份，再落盘；无命中时源文件字节完全不变
		if hits == 0 {
			item.Status, item.Message = "skipped", noMatchMessage(j.src, req, "没有找到匹配内容")
			return jobResult{item: item}
		}
		bak, err := makeBackup(manifestPath, j.src)
		if err != nil {
			return fail("备份原文件失败：" + err.Error())
		}
		if err := os.Remove(j.src); err != nil {
			_ = os.Remove(bak)
			return fail("替换原文件失败：" + err.Error())
		}
		if err := os.Rename(tmp, j.src); err != nil {
			_ = copyFile(bak, j.src) // 回滚，保证不丢文件
			_ = os.Remove(bak)
			return fail("写回文件失败：" + err.Error())
		}
		item.Status, item.Message = "ok", fmt.Sprintf("替换了 %d 处", hits)
		return jobResult{item: item, entry: &UndoEntry{Kind: "inplace", Original: j.src, Produced: j.src, Backup: bak}}
	}

	// 另存：源文件完全不动，产出到目标目录
	// v3.1.3：「另存」= 源文件夹的完整副本。即使内容和文件名都没命中，
	// 也原样复制一份（字节级保真），不再跳过——否则输出目录里的文件
	// 会比源目录少一大截，用户会以为文件丢了。
	unchanged := hits == 0 && !j.nameChanged
	if hits == 0 {
		// 内容没命中（不论文件名是否变化）：复制源文件，保证字节级保真
		_ = os.Remove(tmp)
		if err := copyFile(j.src, tmp); err != nil {
			return fail("复制源文件失败：" + err.Error())
		}
	}
	if _, err := os.Stat(j.out); err == nil {
		if !req.Overwrite {
			item.Status, item.Message = "skipped", "另存目标已存在，跳过（不覆盖）"
			item.Output = j.out
			return jobResult{item: item}
		}
		if err := os.Remove(j.out); err != nil {
			return fail("覆盖目标文件失败：" + err.Error())
		}
	}
	if err := os.Rename(tmp, j.out); err != nil {
		return fail("另存写入失败：" + err.Error())
	}
	if unchanged {
		item.Status, item.Message = "ok", "已原样复制（无改动） → " + j.out
	} else {
		item.Status, item.Message = "ok", "已另存 → " + j.out
	}
	item.Output = j.out
	return jobResult{item: item, entry: &UndoEntry{Kind: "saveas", Original: j.src, Produced: j.out}}
}

// copyOnlyJob 「只替换文件名 + 另存」时把文件原样复制到新名字
func copyOnlyJob(j fileJob, req ReplaceReq) jobResult {
	item := Item{Path: j.src, Kind: "saveas"}
	// v3.1.3：文件名没变也原样复制，保证另存目录是完整副本
	if req.DryRun {
		item.Status, item.Message = "skipped", "预览：将另存为 "+j.out
		item.Output = j.out
		return jobResult{item: item}
	}
	if _, err := os.Stat(j.out); err == nil {
		if !req.Overwrite {
			item.Status, item.Message = "skipped", "另存目标已存在，跳过（不覆盖）"
			item.Output = j.out
			return jobResult{item: item}
		}
		_ = os.Remove(j.out)
	}
	if err := os.MkdirAll(filepath.Dir(j.out), 0755); err != nil {
		item.Status, item.Message = "failed", "创建目录失败：" + err.Error()
		return jobResult{item: item}
	}
	if err := copyFile(j.src, j.out); err != nil {
		item.Status, item.Message = "failed", "复制失败：" + err.Error()
		return jobResult{item: item}
	}
	if j.nameChanged {
		item.Status, item.Message, item.Output = "ok", "已另存 → "+j.out, j.out
	} else {
		item.Status, item.Message, item.Output = "ok", "已原样复制（无改动） → "+j.out, j.out
	}
	return jobResult{item: item, entry: &UndoEntry{Kind: "saveas", Original: j.src, Produced: j.out}}
}

// renameJob 原地模式下的文件名替换
func renameJob(path string, req ReplaceReq) jobResult {
	item := Item{Path: path, Kind: "rename"}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	newStem, hits := applyPairs(stem, req.Pairs)
	if hits == 0 {
		return jobResult{item: item}
	}
	newName := newStem + ext
	newPath := filepath.Join(filepath.Dir(path), newName)
	item.FromName, item.ToName = base, newName
	if newPath == path {
		return jobResult{item: item}
	}
	if req.DryRun {
		item.Status, item.Message = "skipped", "预览：将改名为 "+newName
		return jobResult{item: item}
	}
	if _, err := os.Stat(newPath); err == nil {
		item.Status, item.Message = "failed", "目标文件名已存在，跳过（不覆盖）"
		return jobResult{item: item}
	}
	if err := os.Rename(path, newPath); err != nil {
		item.Status, item.Message = "failed", "改名失败："+err.Error()
		return jobResult{item: item}
	}
	item.Status, item.Message = "ok", "已改名为 " + newName
	return jobResult{item: item, entry: &UndoEntry{
		Kind: "rename", Original: path, Produced: newPath, RenamedTo: newName,
	}}
}

// renameFile 保留旧接口，供既有测试与调用方使用
func renameFile(path string, pairs []Pair) Item {
	return renameJob(path, ReplaceReq{Pairs: pairs}).item
}

// scanFiles 扫描文件夹里的所有目标文件
func scanFiles(req ReplaceReq) ([]string, error) {
	info, err := os.Stat(req.Dir)
	if err != nil {
		return nil, fmt.Errorf("文件夹不存在或无法访问：%w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("填写的路径不是文件夹：%s", req.Dir)
	}
	// v3.1.3：只有当另存目录位于源目录「内部」时才需要排除，避免自产自销。
	// 若另存目录是源目录的父目录（例如源=桌面\AAA，另存=桌面），
	// 旧逻辑会把源目录的所有子文件夹误判成「另存目录」而 SkipDir，导致漏文件。
	skipDir := ""
	if req.SaveAs != "" {
		rel, e := filepath.Rel(filepath.Clean(req.Dir), filepath.Clean(req.SaveAs))
		if e == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			skipDir = filepath.Clean(req.SaveAs)
		}
	}
	var files []string
	err = filepath.WalkDir(req.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的条目
		}
		if d.IsDir() {
			if path != req.Dir {
				if !req.Recursive {
					return filepath.SkipDir
				}
				if skipDir != "" {
					p := filepath.Clean(path)
					if samePath(p, skipDir) || strings.HasPrefix(strings.ToLower(p), strings.ToLower(skipDir)+string(os.PathSeparator)) {
						return filepath.SkipDir // 另存目录不参与扫描，避免自产自销
					}
				}
				if strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir // 跳过隐藏目录
				}
			}
			return nil
		}
		// Office / WPS 打开文档时生成的临时锁文件（~$xxx / $xxx）和隐藏文件
		// 不是用户文档，且常被独占打开，会把统计刷成「失败」。
		base := d.Name()
		if strings.HasPrefix(base, "~$") || strings.HasPrefix(base, "$") || strings.HasPrefix(base, ".") {
			return nil
		}
		if kindOfFile(path) == "" {
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.Size() > maxFileSize {
			return nil // 超大文件跳过
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
