package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

func validateWindowsExecutable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 1024 {
		return fmt.Errorf("文件过小 (%d bytes)", info.Size())
	}
	header := make([]byte, 64)
	if _, err := io.ReadFull(f, header); err != nil {
		return err
	}
	if string(header[:2]) != "MZ" {
		return fmt.Errorf("不是 Windows PE 文件")
	}
	peOffset := int64(binary.LittleEndian.Uint32(header[0x3c:0x40]))
	if peOffset < 64 || peOffset > info.Size()-4 {
		return fmt.Errorf("PE 头偏移无效")
	}
	if _, err := f.Seek(peOffset, io.SeekStart); err != nil {
		return err
	}
	signature := make([]byte, 4)
	if _, err := io.ReadFull(f, signature); err != nil {
		return err
	}
	if string(signature) != "PE\x00\x00" {
		return fmt.Errorf("PE 签名无效")
	}
	return nil
}

func quoteBatchValue(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

func buildWindowsUpdateScript(currentExe, stagedExe, versionFile string) string {
	return fmt.Sprintf(`@echo off
setlocal
set "CURRENT=%s"
set "STAGED=%s"
set "BACKUP=%s.old"
set "VERSION_FILE=%s"
for /L %%%%I in (1,1,60) do (
  move /Y "%%CURRENT%%" "%%BACKUP%%" >nul 2>&1 && goto replace
  timeout /t 1 /nobreak >nul
)
goto failed
:replace
move /Y "%%STAGED%%" "%%CURRENT%%" >nul 2>&1 || goto rollback
del /Q "%%VERSION_FILE%%" >nul 2>&1
start "" "%%CURRENT%%"
timeout /t 2 /nobreak >nul
del /Q "%%BACKUP%%" >nul 2>&1
del /Q "%%~f0" >nul 2>&1
exit /b 0
:rollback
move /Y "%%BACKUP%%" "%%CURRENT%%" >nul 2>&1
:failed
if exist "%%CURRENT%%" start "" "%%CURRENT%%"
del /Q "%%STAGED%%" >nul 2>&1
del /Q "%%~f0" >nul 2>&1
exit /b 1
`, quoteBatchValue(currentExe), quoteBatchValue(stagedExe), quoteBatchValue(currentExe), quoteBatchValue(versionFile))
}
