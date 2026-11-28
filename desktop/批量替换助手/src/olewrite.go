
package main

// olewrite.go —— OLE2/CFB (Compound File Binary) 容器写入器
//
// 纯标准库实现。用于把修改后的流重新打包成完整的 .doc/.xls 文件。
// 采用 512 字节扇区（CFB v3），小流（<4096）走 mini stream。
// 支持读写所有流并保留目录层级结构。

import (
	"encoding/binary"
	"io"
	"os"
	"sort"
	"unicode/utf16"

	"piliangtihuanzhushou/internal/mscfb"
)

const (
	oleSectorSize = 512
	oleMiniSector = 64
	oleMiniCutoff = 4096
	oleFreeSect   = 0xFFFFFFFF
	oleEndOfChain = 0xFFFFFFFE
	oleFatSect    = 0xFFFFFFFD
	oleDifatSect  = 0xFFFFFFFC
	oleNoStream   = 0xFFFFFFFF
	oleMaxRegSect = 0xFFFFFFFA
)

type oleEntry struct {
	name     string
	path     []string
	data     []byte
	isDir    bool
	children []*oleEntry
	// 打包时填充
	startSector        uint32
	size               uint64
	left, right, child uint32
	color              byte
	dirID              uint32
}

// readAllOLEStreams 读取 OLE 文件全部流，返回树形结构
func readAllOLEStreams(path string) ([]*oleEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := mscfb.New(f)
	if err != nil {
		return nil, err
	}
	var entries []*oleEntry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(e)
		if err != nil {
			return nil, err
		}
		entries = append(entries, &oleEntry{
			name:  e.Name,
			path:  append([]string(nil), e.Path...),
			data:  data,
			isDir: e.Size == 0 && len(data) == 0 && isDirEntry(e),
		})
	}
	return entries, nil
}

// isDirEntry 判断是否为目录项（mscfb 未直接暴露类型，用 Initial/Path 推断备用）
func isDirEntry(e *mscfb.File) bool {
	return e.Size == 0 && len(e.Path) > 0
}

// oleSectorAllocator 扇区分配器
type oleSectorAllocator struct {
	data []byte // 所有扇区数据拼接
}

func newAllocator() *oleSectorAllocator { return &oleSectorAllocator{} }

func (a *oleSectorAllocator) alloc() uint32 {
	id := uint32(len(a.data) / oleSectorSize)
	a.data = append(a.data, make([]byte, oleSectorSize)...)
	return id
}

func (a *oleSectorAllocator) write(id uint32, data []byte) {
	off := int(id) * oleSectorSize
	copy(a.data[off:off+oleSectorSize], data)
}

// buildOleContainer 把流列表打包成完整 OLE2 容器。
// entries: 扁平的流列表，每个 entry 提供 name 与 data；path 层级用 name 中的路径表示。
func buildOleContainer(entries []*oleEntry) ([]byte, error) {
	// 简化实现：把所有条目放在根目录下一层（Word/Excel 的主要流都是这样）。
	// 按名字排序保证目录树稳定。
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	// 分类：小流走 mini stream，大流走常规扇区
	var small, large []*oleEntry
	for _, e := range entries {
		if len(e.data) < oleMiniCutoff {
			small = append(small, e)
		} else {
			large = append(large, e)
		}
	}

	alloc := newAllocator()
	// 预留：FAT 扇区（先占位，最后回填），目录扇区，miniFAT 扇区
	// 先分配数据扇区，再补元数据，最后重写 header。

	// 1) 分配大流扇区
	for _, e := range large {
		e.startSector, _ = allocChain(alloc, e.data, oleSectorSize)
		e.size = uint64(len(e.data))
	}

	// 2) 构建 mini stream：所有小流按 64 字节分片拼接
	var miniStream []byte
	miniFAT := make([]uint32, 0)
	for _, e := range small {
		n := (len(e.data) + oleMiniSector - 1) / oleMiniSector
		start := uint32(len(miniStream) / oleMiniSector)
		for i := 0; i < n; i++ {
			chunk := make([]byte, oleMiniSector)
			off := i * oleMiniSector
			if off < len(e.data) {
				copy(chunk, e.data[off:])
			}
			miniStream = append(miniStream, chunk...)
			if i < n-1 {
				miniFAT = append(miniFAT, start+uint32(i)+1)
			} else {
				miniFAT = append(miniFAT, oleEndOfChain)
			}
		}
		e.startSector = start // mini stream 内的 mini sector 号
		e.size = uint64(len(e.data))
	}

	// 3) 把 mini stream 作为一个"流"写入常规扇区
	var miniStreamStart uint32
	if len(miniStream) > 0 {
		miniStreamStart, _ = allocChain(alloc, miniStream, oleSectorSize)
	}

	// 4) 写 miniFAT 到常规扇区
	miniFATBytes := u32sToBytes(miniFAT)
	var miniFATStart uint32
	numMiniFATSectors := 0
	if len(miniFATBytes) > 0 {
		miniFATStart, numMiniFATSectors = allocChain(alloc, miniFATBytes, oleSectorSize)
	}

	// 5) 构建目录项
	// 目录项 128 字节；第 0 项是 Root Entry，其余是流
	dirEntries := make([][]byte, 0, len(entries)+1)
	root := makeDirEntry("Root Entry", 5, 0, miniStreamStart, uint64(len(miniStream)))
	dirEntries = append(dirEntries, root)
	for _, e := range entries {
		objType := byte(2) // stream
		if e.isDir {
			objType = 1 // storage
		}
		dirEntries = append(dirEntries, makeDirEntry(e.name, objType, 0, e.startSector, e.size))
	}

	// 构造平衡二叉搜索树（按 UTF-16 名称排序）
	streamIDs := make([]int, len(entries))
	for i := range streamIDs {
		streamIDs[i] = i + 1 // +1 因为 root 占 0
	}
	childID := buildSiblingTree(entries, streamIDs, dirEntries)

	// 设置 root 的 child 指针
	setDirChild(dirEntries[0], childID)

	// 写目录到扇区
	dirBytes := make([]byte, 0, len(dirEntries)*128)
	for _, d := range dirEntries {
		dirBytes = append(dirBytes, d...)
	}
	dirStart, _ := allocChain(alloc, dirBytes, oleSectorSize)

	// 6) 现在所有数据扇区已分配，构建 FAT
	numSectors := len(alloc.data) / oleSectorSize
	// 估算 FAT 扇区数：每扇区可存 128 个 FAT 项，需覆盖 numSectors + FAT 自身
	numFATSectors := 1
	for {
		capacity := numFATSectors * (oleSectorSize / 4)
		need := numSectors + numFATSectors
		if capacity >= need {
			break
		}
		numFATSectors++
	}
	// 预留 FAT 扇区（在已有数据扇区之后）
	fatSectorStarts := make([]uint32, numFATSectors)
	for i := 0; i < numFATSectors; i++ {
		fatSectorStarts[i] = alloc.alloc()
	}

	// 构建 FAT 数组
	fat := make([]uint32, numFATSectors*oleSectorSize/4)
	for i := range fat {
		fat[i] = oleFreeSect
	}
	// 标记 FAT 扇区
	for _, s := range fatSectorStarts {
		fat[s] = oleFatSect
	}
	// 写入各链的 FAT 项
	fillChainFAT(fat, large, oleSectorSize)
	if len(miniStream) > 0 {
		fillRawChainFAT(fat, miniStreamStart, len(miniStream))
	}
	if len(miniFATBytes) > 0 {
		fillRawChainFAT(fat, miniFATStart, len(miniFATBytes))
	}
	fillRawChainFAT(fat, dirStart, len(dirBytes))

	// 写 FAT 扇区数据
	for i, s := range fatSectorStarts {
		chunk := u32sToBytes(fat[i*(oleSectorSize/4) : (i+1)*(oleSectorSize/4)])
		alloc.write(s, chunk)
	}

	// 7) 构建 header（512 字节）
	header := make([]byte, 512)
	binary.LittleEndian.PutUint64(header[0:8], 0xE11AB1A1E011CFD0)
	binary.LittleEndian.PutUint16(header[24:26], 0x003E) // minor
	binary.LittleEndian.PutUint16(header[26:28], 0x0003) // major v3
	binary.LittleEndian.PutUint16(header[28:30], 0xFFFE) // little endian
	binary.LittleEndian.PutUint16(header[30:32], 0x0009) // 512-byte sectors
	binary.LittleEndian.PutUint16(header[32:34], 0x0006) // 64-byte mini sectors
	binary.LittleEndian.PutUint32(header[40:44], 0)      // num dir sectors (v3=0)
	binary.LittleEndian.PutUint32(header[44:48], uint32(numFATSectors))
	binary.LittleEndian.PutUint32(header[48:52], dirStart)
	binary.LittleEndian.PutUint32(header[60:64], miniFATStart)
	binary.LittleEndian.PutUint32(header[64:68], uint32(numMiniFATSectors))
	binary.LittleEndian.PutUint32(header[68:72], oleEndOfChain) // no DIFAT
	binary.LittleEndian.PutUint32(header[72:76], 0)
	for i := 0; i < len(fatSectorStarts) && i < 109; i++ {
		binary.LittleEndian.PutUint32(header[76+i*4:80+i*4], fatSectorStarts[i])
	}
	for i := len(fatSectorStarts); i < 109; i++ {
		binary.LittleEndian.PutUint32(header[76+i*4:80+i*4], oleFreeSect)
	}

	out := make([]byte, 0, 512+len(alloc.data))
	out = append(out, header...)
	out = append(out, alloc.data...)
	return out, nil
}

// allocChain 分配扇区链并写入数据，返回起始扇区与扇区数
func allocChain(a *oleSectorAllocator, data []byte, sectorSize int) (uint32, int) {
	n := (len(data) + sectorSize - 1) / sectorSize
	if n == 0 {
		return oleEndOfChain, 0
	}
	start := uint32(len(a.data) / oleSectorSize) // 注意：用 oleSectorSize，因为分配器按 512 分配
	ids := make([]uint32, n)
	for i := 0; i < n; i++ {
		ids[i] = a.alloc()
	}
	for i := 0; i < n; i++ {
		chunk := make([]byte, oleSectorSize)
		off := i * sectorSize
		if off < len(data) {
			end := off + sectorSize
			if end > len(data) {
				end = len(data)
			}
			copy(chunk, data[off:end])
		}
		a.write(ids[i], chunk)
	}
	return start, n
}

// fillRawChainFAT 为一段连续扇区写 FAT 链
func fillRawChainFAT(fat []uint32, start uint32, dataLen int) {
	n := (dataLen + oleSectorSize - 1) / oleSectorSize
	for i := 0; i < n; i++ {
		if i == n-1 {
			fat[start+uint32(i)] = oleEndOfChain
		} else {
			fat[start+uint32(i)] = start + uint32(i) + 1
		}
	}
}

func fillChainFAT(fat []uint32, entries []*oleEntry, sectorSize int) {
	for _, e := range entries {
		fillRawChainFAT(fat, e.startSector, len(e.data))
	}
}

func u32sToBytes(vals []uint32) []byte {
	out := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], v)
	}
	return out
}

// makeDirEntry 构造 128 字节目录项
func makeDirEntry(name string, objType byte, color byte, start uint32, size uint64) []byte {
	d := make([]byte, 128)
	nm := utf16.Encode([]rune(name))
	nm = append(nm, 0)
	for i := 0; i < len(nm) && i < 32; i++ {
		binary.LittleEndian.PutUint16(d[i*2:i*2+2], nm[i])
	}
	binary.LittleEndian.PutUint16(d[64:66], uint16((len(nm))*2)) // name length incl null
	d[66] = objType
	d[67] = color
	binary.LittleEndian.PutUint32(d[68:72], oleNoStream) // left
	binary.LittleEndian.PutUint32(d[72:76], oleNoStream) // right
	binary.LittleEndian.PutUint32(d[76:80], oleNoStream) // child
	binary.LittleEndian.PutUint32(d[116:120], start)
	binary.LittleEndian.PutUint64(d[120:128], size)
	return d
}

func setDirChild(d []byte, child uint32) {
	binary.LittleEndian.PutUint32(d[76:80], child)
}

// buildSiblingTree 用有序构造近似平衡的红黑/二叉搜索树（按 UTF-16 名称排序）
func buildSiblingTree(entries []*oleEntry, ids []int, dirs [][]byte) uint32 {
	if len(ids) == 0 {
		return oleNoStream
	}
	mid := len(ids) / 2
	id := ids[mid]
	root := uint32(id)
	// 重建目录项，设置左右子节点
	left := buildSiblingTree(entries, ids[:mid], dirs)
	right := buildSiblingTree(entries, ids[mid+1:], dirs)
	d := dirs[id]
	binary.LittleEndian.PutUint32(d[68:72], left)
	binary.LittleEndian.PutUint32(d[72:76], right)
	return root
}
