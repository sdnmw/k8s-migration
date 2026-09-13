package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type AssessmentRepository struct{ pool *pgxpool.Pool }

func NewAssessmentRepository(pool *pgxpool.Pool) *AssessmentRepository {
	return &AssessmentRepository{pool: pool}
}

func (r *AssessmentRepository) Create(ctx context.Context, value assessment.Assessment) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin assessment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO assessments
		(id,application_id,score,blocker_count,warning_count,info_count,status,created_at,completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.ID, value.ApplicationID, value.Score,
		value.BlockerCount, value.WarningCount, value.InfoCount, value.Status, value.CreatedAt, value.CompletedAt); err != nil {
		return fmt.Errorf("insert assessment: %w", mapError(err))
	}
	for _, issue := range value.Issues {
		if _, err = tx.Exec(ctx, `INSERT INTO assessment_issues
			(id,assessment_id,severity,category,resource_kind,resource_namespace,resource_name,rule_id,title,description,remediation,auto_fixable)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, issue.ID, value.ID, issue.Severity,
			issue.Category, issue.ResourceKind, issue.ResourceNamespace, issue.ResourceName, issue.RuleID,
			issue.Title, issue.Description, issue.Remediation, issue.AutoFixable); err != nil {
			return fmt.Errorf("insert assessment issue: %w", mapError(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit assessment transaction: %w", err)
	}
	return nil
}

func (r *AssessmentRepository) Get(ctx context.Context, id uuid.UUID) (assessment.Assessment, error) {
	var value assessment.Assessment
	err := r.pool.QueryRow(ctx, `SELECT id,application_id,score,blocker_count,warning_count,info_count,status,created_at,completed_at
		FROM assessments WHERE id=$1`, id).Scan(&value.ID, &value.ApplicationID, &value.Score, &value.BlockerCount,
		&value.WarningCount, &value.InfoCount, &value.Status, &value.CreatedAt, &value.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return assessment.Assessment{}, repository.ErrNotFound
	}
	if err != nil {
		return assessment.Assessment{}, fmt.Errorf("get assessment: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT id,assessment_id,severity,category,resource_kind,resource_namespace,resource_name,rule_id,title,description,remediation,auto_fixable
		FROM assessment_issues WHERE assessment_id=$1
		ORDER BY severity,category,rule_id,resource_kind,resource_namespace,resource_name,id`, id)
	if err != nil {
		return assessment.Assessment{}, fmt.Errorf("list assessment issues: %w", err)
	}
	defer rows.Close()
	value.Issues = make([]assessment.Issue, 0)
	for rows.Next() {
		var issue assessment.Issue
		if err := rows.Scan(&issue.ID, &issue.AssessmentID, &issue.Severity, &issue.Category, &issue.ResourceKind,
			&issue.ResourceNamespace, &issue.ResourceName, &issue.RuleID, &issue.Title, &issue.Description,
			&issue.Remediation, &issue.AutoFixable); err != nil {
			return assessment.Assessment{}, fmt.Errorf("scan assessment issue: %w", err)
		}
		value.Issues = append(value.Issues, issue)
	}
	if err := rows.Err(); err != nil {
		return assessment.Assessment{}, fmt.Errorf("iterate assessment issues: %w", err)
	}
	return value, nil
}
