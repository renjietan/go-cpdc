- 【启动参数】
  - log-level: 
- 步骤
  - 通过【启动参数】，设置日志等级，默认 info
    - 自更新重启后，通过【启动参数】中，获取参数文件路径，在从参数文件中获取参数，如果未传入【启动参数】，则不做任何处理，自然启动, 参数如下：
      type FinalizeParams struct { 
        OldPID          int      `json:"old_pid"`                                   // 
        OldExe          string   `json:"old_exe"`                                   // 
        NewExe          string   `json:"new_exe"`                                   // 
        BackupExe       string   `json:"backup_exe"`                                // 
        RunCommand      []string `json:"run_command"`                               // 
        LogPath         string   `json:"log_path"`                                  // 
        FinalizeLogPath string   `json:"finalize_log_path"`                         // 
        FailMarker      string   `json:"fail_marker"`                               // 
        DataDir         string   `json:"data_dir"`                                  // 
        OldVersion      string   `json:"old_version"`                               // 
        ConfigPath      string   `json:"config_path"`                               // 
        FileCRC         uint32   `json:"file_crc"`                                  // 
        FilePacketCount uint32   `json:"file_packet_count"`                         // 
        FileTotalBytes  uint32   `json:"file_total_bytes"`                          // 
        FileName        string   `json:"file_name"`                                 // 
      }
  - 