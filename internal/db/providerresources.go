package db

import "database/sql"

func (d *DB) UpsertProviderResource(r *ProviderResource) error {
	_, err := d.sql.Exec(`
		INSERT INTO provider_resources
			(id, deployment_id, provider, app_name, machine_id, region, image_ref, image_label)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(deployment_id) DO UPDATE SET
			provider    = excluded.provider,
			app_name    = excluded.app_name,
			machine_id  = excluded.machine_id,
			region      = excluded.region,
			image_ref   = excluded.image_ref,
			image_label = excluded.image_label`,
		r.ID, r.DeploymentID, r.Provider, r.AppName, r.MachineID,
		r.Region, r.ImageRef, r.ImageLabel,
	)
	return err
}

func (d *DB) GetProviderResource(deploymentID string) (*ProviderResource, error) {
	r := &ProviderResource{}
	var createdAt string
	err := d.sql.QueryRow(`
		SELECT id, deployment_id, provider, app_name, machine_id, region, image_ref, image_label, created_at
		FROM provider_resources WHERE deployment_id = ?`, deploymentID).
		Scan(&r.ID, &r.DeploymentID, &r.Provider, &r.AppName, &r.MachineID,
			&r.Region, &r.ImageRef, &r.ImageLabel, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseTime(createdAt)
	return r, nil
}
