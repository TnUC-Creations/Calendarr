; Calendarr Windows Installer — Inno Setup Script
; To build: open this file in Inno Setup IDE and click Build → Compile
; Prerequisites: calendarr.exe must exist in the same folder as this script.

#define AppName "Calendarr"
#define AppVersion "1.7.0"
#define AppPublisher "TnUC Creations"
#define AppURL "https://github.com/TnUC-Creations/Calendarr"
#define AppExeName "calendarr.exe"
#define DataDir "{commonappdata}\Calendarr"
#define InstallerDir "installer"

[Setup]
AppId={{A3F2B1C4-9D7E-4F6A-8B2C-5E1D3A7F9C0B}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
AppVerName={#AppName} {#AppVersion}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=no
OutputDir=.
OutputBaseFilename=calendarr-setup-{#AppVersion}
SetupIconFile={#InstallerDir}\calendarr.ico
WizardImageFile={#InstallerDir}\wizard-image.png
WizardSmallImageFile={#InstallerDir}\wizard-small.png
WizardImageStretch=no
Compression=lzma
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=yes
UninstallDisplayIcon={app}\{#AppExeName}
MinVersion=6.1sp1
VersionInfoVersion={#AppVersion}.0
VersionInfoCompany={#AppPublisher}
VersionInfoDescription={#AppName} Setup
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#AppVersion}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
WelcomeLabel1=Welcome to the Calendarr Setup Wizard
WelcomeLabel2=Calendarr installs as a Windows service and keeps Radarr and Sonarr release dates synced to Google Calendar.%n%nSetup will install Calendarr and start the local web dashboard when finished.
SelectTasksDesc=Choose the shortcuts Setup should create.
FinishedHeadingLabel=Calendarr is ready
FinishedLabelNoIcons=Calendarr has been installed and the Windows service has been started.%n%nOpen http://localhost:5000 to connect Radarr, Sonarr, and Google Calendar.
FinishedLabel=Calendarr has been installed and the Windows service has been started.%n%nOpen http://localhost:5000 to connect Radarr, Sonarr, and Google Calendar.

[Files]
Source: "{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#InstallerDir}\calendarr.ico"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
; Create the data directory where config, logs, and history are stored.
Name: "{#DataDir}"; Permissions: everyone-full

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Icons]
; Start Menu folder
Name: "{group}\Open Calendarr";     Filename: "http://localhost:5000"; \
    Comment: "Open the Calendarr web interface"; IconFilename: "{app}\calendarr.ico"
Name: "{group}\Uninstall Calendarr"; Filename: "{uninstallexe}"
Name: "{autodesktop}\Calendarr"; Filename: "http://localhost:5000"; \
    Comment: "Open the Calendarr web interface"; IconFilename: "{app}\calendarr.ico"; \
    Tasks: desktopicon

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
