package db

func (d *DB) AppendBuildLog(log *BuildLog) error {
	_, err := d.sql.Exec(`
		INSERT INTO build_logs (id, deployment_id, log_type, content)
		VALUES (?, ?, ?, ?)`,
		log.ID, log.DeploymentID, log.LogType, log.Content,
	)
	return err
}

func (d *DB) GetBuildLogs(deploymentID, logType string) ([]*BuildLog, error) {
	rows, err := d.sql.Query(`
		SELECT id, deployment_id, log_type, content, created_at
		FROM build_logs
		WHERE deployment_id = ? AND (? = '' OR log_type = ?)
		ORDER BY created_at ASC`,
		deploymentID, logType, logType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BuildLog
	for rows.Next() {
		l := &BuildLog{}
		var createdAt string
		if err := rows.Scan(&l.ID, &l.DeploymentID, &l.LogType, &l.Content, &createdAt); err != nil {
			return nil, err
		}
		l.CreatedAt = parseTime(createdAt)
		out = append(out, l)
	}
	return out, rows.Err()
}
