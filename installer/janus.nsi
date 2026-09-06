; Janus NSIS Installer Script
; Requires NSIS 3.x — https://nsis.sourceforge.io/
;
; Build:
;   makensis installer\janus.nsi
;
; Produces: installer\JanusSetup-0.1.0.exe

!define APPNAME     "Janus"
!define APPVERSION  "0.1.0"
!define PUBLISHER   "Janus AI, LLC"
!define APPURL      "https://janus-ai.com"
!define INSTALLDIR  "$PROGRAMFILES64\Janus"
!define UNINSTKEY   "Software\Microsoft\Windows\CurrentVersion\Uninstall\Janus"

; ── Metadata ──────────────────────────────────────────────────────────────────
Name              "${APPNAME} ${APPVERSION}"
OutFile           "JanusSetup-${APPVERSION}.exe"
InstallDir        "${INSTALLDIR}"
InstallDirRegKey  HKLM "${UNINSTKEY}" "InstallLocation"
RequestExecutionLevel admin
SetCompressor     /SOLID lzma
Unicode           True

; ── Modern UI ─────────────────────────────────────────────────────────────────
!include "MUI2.nsh"

!define MUI_ABORTWARNING
!define MUI_ICON   "assets\janus.ico"
!define MUI_UNICON "assets\janus.ico"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; ── Sections ──────────────────────────────────────────────────────────────────
Section "Janus (required)" SecMain
    SectionIn RO

    SetOutPath "$INSTDIR"
    File /r "..\dist\*.*"

    ; ── webui ──────────────────────────────────────────────────────────────────
    File "..\webui.html"

    ; ── .env.example ───────────────────────────────────────────────────────────
    IfFileExists "$INSTDIR\.env" env_exists
        File /oname=.env "..\env.example"
    env_exists:

    ; ── Create required directories ────────────────────────────────────────────
    CreateDirectory "$INSTDIR\logs"
    CreateDirectory "$INSTDIR\models"
    CreateDirectory "$INSTDIR\data"
    CreateDirectory "$INSTDIR\workspace"

    ; ── Start Menu shortcut ────────────────────────────────────────────────────
    CreateDirectory "$SMPROGRAMS\${APPNAME}"
    CreateShortcut  "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" \
                    "$INSTDIR\janus.exe" "" "$INSTDIR\janus.exe" 0
    CreateShortcut  "$SMPROGRAMS\${APPNAME}\Uninstall.lnk" \
                    "$INSTDIR\Uninstall.exe"

    ; ── Desktop shortcut ───────────────────────────────────────────────────────
    CreateShortcut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\janus.exe" "" "$INSTDIR\janus.exe" 0

    ; ── Registry ───────────────────────────────────────────────────────────────
    WriteRegStr   HKLM "${UNINSTKEY}" "DisplayName"      "${APPNAME}"
    WriteRegStr   HKLM "${UNINSTKEY}" "DisplayVersion"   "${APPVERSION}"
    WriteRegStr   HKLM "${UNINSTKEY}" "Publisher"        "${PUBLISHER}"
    WriteRegStr   HKLM "${UNINSTKEY}" "URLInfoAbout"     "${APPURL}"
    WriteRegStr   HKLM "${UNINSTKEY}" "InstallLocation"  "$INSTDIR"
    WriteRegStr   HKLM "${UNINSTKEY}" "UninstallString"  "$INSTDIR\Uninstall.exe"
    WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify"         1
    WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair"         1

    ; ── Uninstaller ────────────────────────────────────────────────────────────
    WriteUninstaller "$INSTDIR\Uninstall.exe"
SectionEnd

; ── Optional: register as Windows Service via NSSM ────────────────────────────
Section /o "Run as Windows Service (NSSM required)" SecService
    ExecWait '"$INSTDIR\nssm.exe" install Janus "$INSTDIR\janus.exe"'
    ExecWait '"$INSTDIR\nssm.exe" set Janus AppDirectory "$INSTDIR"'
    ExecWait '"$INSTDIR\nssm.exe" set Janus Start SERVICE_AUTO_START'
    ExecWait '"$INSTDIR\nssm.exe" start Janus'
SectionEnd

; ── Uninstaller ───────────────────────────────────────────────────────────────
Section "Uninstall"
    ; Stop service if running
    ExecWait '"$INSTDIR\nssm.exe" stop Janus'   ; silently fails if not installed
    ExecWait '"$INSTDIR\nssm.exe" remove Janus confirm'

    Delete "$INSTDIR\janus.exe"
    Delete "$INSTDIR\keygen.exe"
    Delete "$INSTDIR\webui.html"
    Delete "$INSTDIR\Uninstall.exe"
    RMDir  /r "$INSTDIR\dist"

    ; Keep logs/, data/, models/ — contain user data
    MessageBox MB_YESNO "Delete logs, data, and model files as well?" IDNO keep_data
        RMDir /r "$INSTDIR\logs"
        RMDir /r "$INSTDIR\data"
        RMDir /r "$INSTDIR\models"
    keep_data:

    RMDir  "$INSTDIR"

    Delete "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk"
    Delete "$SMPROGRAMS\${APPNAME}\Uninstall.lnk"
    RMDir  "$SMPROGRAMS\${APPNAME}"
    Delete "$DESKTOP\${APPNAME}.lnk"

    DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
