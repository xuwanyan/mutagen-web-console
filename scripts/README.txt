Mutagen Web Agent - Installation Guide
========================================

1. Extract all files to a folder on the target machine

2. Right-click install.bat > Run as Administrator
   This will:
   - Copy files to C:\mutagen\
   - Register auto-start task (starts when you log in)

3. Register as Windows service (optional, for auto-start at boot)
   Run the following command as Administrator:

   sc create MutagenWebAgent binPath= "C:\mutagen\mutagen-web-agent.exe --config C:\mutagen\agent-config.json -log C:\mutagen\agent.log" start= auto obj= ".\rpa" password= "YOUR_PASSWORD"

   Replace "YOUR_PASSWORD" with your Windows login password.

4. Start the service:
   sc start MutagenWebAgent

========================================
Files:
  mutagen.exe               - Mutagen sync engine
  mutagen-web-agent.exe     - Agent binary
  agent-config.json         - Agent configuration
  install.bat               - Installation script
  README.txt                - This file
========================================
Management:
  Start:   sc start MutagenWebAgent
  Stop:    sc stop MutagenWebAgent
  Status:  sc query MutagenWebAgent
  Logs:    C:\mutagen\agent.log
========================================
