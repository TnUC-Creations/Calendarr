; Calendarr Windows Installer — Inno Setup Script
; To build: open this file in Inno Setup IDE and click Build → Compile
; Prerequisites: calendarr.exe must exist in the same folder as this script.

#define AppName "Calendarr"
#define AppVersion "1.5.5"
#define AppPublisher "TnUC Creations"
#define AppURL "https://github.com/TnUC-Creations/Calendarr"
#define AppExeName "calendarr.exe"
#define DataDir "{commonappdata}\Calendarr"

[Setup]
AppId={{A3F2B1C4-9D7E-4F6A-8B2C-5E1D3A7F9C0B}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=no
OutputDir=.
OutputBaseFilename=calendarr-setup-{#AppVersion}
Compression=lzma
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64
CloseApplications=yes
UninstallDisplayIcon={app}\{#AppExeName}
MinVersion=6.1

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
; Create the data directory where config, logs, and history are stored.
Name: "{#DataDir}"; Permissions: everyone-full

[Icons]
; Start Menu folder
Name: "{group}\Open Calendarr";     Filename: "http://localhost:5000"; \
    Comment: "Open the Calendarr web interface"
Name: "{group}\Uninstall Calendarr"; Filename: "{uninstallexe}"

[Run]
; Register the Windows service pointing to the data directory.
Filename: "{app}\{#AppExeName}"; Parameters: "--install --data ""{#DataDir}"""; \
    Flags: runhidden waituntilterminated; \
    StatusMsg: "Registering Windows service..."; \
    Description: "Register Calendarr as a Windows service"

; Start the service.
Filename: "net"; Parameters: "start Calendarr"; \
    Flags: runhidden waituntilterminated shellexec; \
    StatusMsg: "Starting Calendarr service..."

; Open the web UI after install (optional — user can untick).
Filename: "http://localhost:5000"; \
    Flags: shellexec runasoriginaluser nowait postinstall skipifsilent; \
    Description: "Open Calendarr in browser"

[UninstallRun]
; Stop the service.
Filename: "{sys}\sc.exe"; Parameters: "stop Calendarr"; \
    Flags: runhidden waituntilterminated; RunOnceId: "StopSvc"

; Delete the service registration.
Filename: "{sys}\sc.exe"; Parameters: "delete Calendarr"; \
    Flags: runhidden waituntilterminated; RunOnceId: "DeleteSvc"

[Code]
// Show a getting-started reminder after a fresh install.
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    MsgBox(
      'Calendarr is installed and running.' + #13#10 + #13#10 +
      'Open http://localhost:5000 to configure Radarr, Sonarr, and connect your Google Calendar.',
      mbInformation, MB_OK
    );
  end;
end;

// On uninstall, ask whether to keep user data.
function InitializeUninstall(): Boolean;
var
  Res: Integer;
begin
  Res := MsgBox(
    'Keep your configuration, history, and log files?' + #13#10 + #13#10 +
    'Click Yes to keep files in:' + #13#10 +
    '  ' + ExpandConstant('{commonappdata}\Calendarr') + #13#10 + #13#10 +
    'Click No to delete everything.',
    mbConfirmation, MB_YESNO
  );
  // We always proceed with uninstall; data removal handled separately.
  if Res = IDNO then
    DelTree(ExpandConstant('{commonappdata}\Calendarr'), True, True, True);
  Result := True;
end;
