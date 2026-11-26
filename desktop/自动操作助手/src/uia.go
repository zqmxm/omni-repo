package main

// 桌面软件后台控制：借助 Windows UI Automation 直接操作目标窗口里的控件。
// 不移动鼠标、不抢占焦点、不发送全局按键，因此不会影响用户正在做的事情。
// PowerShell 脚本只作为调用 UIA 的载体，运行窗口被隐藏。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func runPowerShell(script string, env map[string]string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("PowerShell 执行失败: %s", text)
		}
		return text, err
	}
	if strings.HasPrefix(text, "错误:") || strings.HasPrefix(text, "未找到") {
		return text, errors.New(text)
	}
	return text, nil
}

const uiaFindHeader = `
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$kw = $env:ZD_TITLE
$root = $null
if ($kw) {
  $desktop = [System.Windows.Automation.AutomationElement]::RootElement
  $wins = $desktop.FindAll([System.Windows.Automation.TreeScope]::Children, [System.Windows.Automation.Condition]::TrueCondition)
  foreach ($w in $wins) {
    if ($w.Current.Name -and $w.Current.Name -like "*$kw*") { $root = $w; break }
  }
}
if (-not $root) {
  $proc = Get-Process | Where-Object { $_.MainWindowTitle -and $_.MainWindowTitle -like "*$kw*" } | Select-Object -First 1
  if ($proc) { $root = [System.Windows.Automation.AutomationElement]::FromHandle($proc.MainWindowHandle) }
}
if (-not $root) { Write-Output "未找到窗口：$kw"; exit 1 }
`

const uiaFindControl = `
$nm = $env:ZD_NAME
$aid = $env:ZD_AID
if ($nm) {
  $cond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, $nm)
} elseif ($aid) {
  $cond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty, $aid)
} else {
  Write-Output "错误: 未填写控件名称或自动化ID"; exit 1
}
$el = $root.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $cond)
if (-not $el) { Write-Output "未找到控件：$nm$aid"; exit 1 }
`

// uiaTestWindow 检查窗口标题关键词能否找到目标窗口
func uiaTestWindow(title string) (string, error) {
	return runPowerShell(uiaFindHeader+`
Write-Output ("找到窗口: " + $root.Current.Name + " | 进程ID: " + $root.Current.ProcessId + " | 类型: " + $root.Current.ControlType.ProgrammaticName)
`, map[string]string{"ZD_TITLE": title})
}

// uiaInvoke 调用控件的 Invoke / DoDefaultAction，不移动鼠标
func uiaInvoke(title, name, automationID string) (string, error) {
	script := uiaFindHeader + uiaFindControl + `
$inv = $null
if ($el.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$inv)) {
  $inv.Invoke()
  Write-Output ("已点击: " + $el.Current.Name)
} else {
  $legacy = $null
  if ($el.TryGetCurrentPattern([System.Windows.Automation.LegacyIAccessiblePattern]::Pattern, [ref]$legacy)) {
    $legacy.DoDefaultAction()
    Write-Output ("已触发默认动作: " + $el.Current.Name)
  } else {
    Write-Output "错误: 该控件不支持点击（Invoke/LegacyIAccessible 均不可用）"; exit 1
  }
}
`
	return runPowerShell(script, map[string]string{
		"ZD_TITLE": title, "ZD_NAME": name, "ZD_AID": automationID,
	})
}

// uiaPressEnter 桌面版"回车"：不发送系统按键，而是直接调用窗口里的默认按钮
// （IsDefaultButton 或名称为 确定/搜索/提交/OK 的按钮），保持后台不占键鼠
func uiaPressEnter(title string) error {
	script := uiaFindHeader + `
$all = $root.FindAll([System.Windows.Automation.TreeScope]::Descendants, [System.Windows.Automation.Condition]::TrueCondition)
$names = @('确定','确认','搜索','提交','OK','Ok','Search','Submit','查询','立即搜索')
$target = $null
foreach ($el in $all) {
  try {
    if ($el.Current.ControlType.ProgrammaticName -ne 'ControlType.Button') { continue }
    if ($el.Current.IsDefaultButton) { $target = $el; break }
    if ($names -contains $el.Current.Name) { $target = $el; break }
  } catch {}
}
if ($null -eq $target) { Write-Output "未找到默认按钮，跳过回车"; exit 0 }
$inv = $null
if ($target.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$inv)) {
  $inv.Invoke(); Write-Output ("已按下: " + $target.Current.Name)
} else { Write-Output "默认按钮不支持 Invoke，跳过回车" }
`
	_, err := runPowerShell(script, map[string]string{"ZD_TITLE": title})
	return err
}

// uiaSetValue 给输入框/编辑控件赋值，不发送按键
func uiaSetValue(title, name, automationID, text string) (string, error) {
	script := uiaFindHeader + uiaFindControl + `
$val = $null
if ($el.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern, [ref]$val)) {
  $val.SetValue($env:ZD_TEXT)
  Write-Output ("已填写: " + $el.Current.Name)
} else {
  $legacy = $null
  if ($el.TryGetCurrentPattern([System.Windows.Automation.LegacyIAccessiblePattern]::Pattern, [ref]$legacy)) {
    $legacy.SetValue($env:ZD_TEXT)
    Write-Output ("已填写(兼容模式): " + $el.Current.Name)
  } else {
    Write-Output "错误: 该控件不支持赋值（ValuePattern/LegacyIAccessible 均不可用）"; exit 1
  }
}
`
	return runPowerShell(script, map[string]string{
		"ZD_TITLE": title, "ZD_NAME": name, "ZD_AID": automationID, "ZD_TEXT": text,
	})
}

// uiaLaunch 启动一个桌面程序（这一步会让程序窗口出现，后续操作不再占用键鼠）
func uiaLaunch(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("程序不存在: %s", path)
	}
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return "已启动程序: " + path, nil
}

// uiaDumpTree 导出窗口的可交互控件树，作为 AI 的“视觉”输入。
// 只取有名称的控件，限制数量以控制 AI token 消耗。
func uiaDumpTree(title string) (string, error) {
	script := uiaFindHeader + `
$all = $root.FindAll([System.Windows.Automation.TreeScope]::Descendants, [System.Windows.Automation.Condition]::TrueCondition)
$n = 0
$lines = New-Object System.Collections.Generic.List[string]
foreach ($el in $all) {
  if ($n -ge 80) { break }
  try {
    $nm = $el.Current.Name
    $ct = $el.Current.ControlType.ProgrammaticName
    $aid = $el.Current.AutomationId
    if (-not $nm) { continue }
    if ($ct -notmatch 'Button|Edit|ComboBox|CheckBox|RadioButton|ListItem|MenuItem|TabItem|Hyperlink|Text') { continue }
    $txt = $nm.Substring(0, [Math]::Min(60, $nm.Length))
    $lines.Add(("控件 " + ($n+1) + ": " + $ct + " 名称=""" + $txt + """ 自动化ID=""" + $aid + """"))
    $n++
  } catch {}
}
if ($n -eq 0) { Write-Output "未发现可交互控件（此软件可能自绘界面，请改用前台录制模式）"; exit 1 }
Write-Output ($lines -join [Environment]::NewLine)
`
	return runPowerShell(script, map[string]string{"ZD_TITLE": title})
}
