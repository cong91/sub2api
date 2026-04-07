package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// APIKeyGrantedGroup holds the edge schema definition for api_key_groups relationship.
type APIKeyGrantedGroup struct {
	ent.Schema
}

func (APIKeyGrantedGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "api_key_groups"},
		field.ID("group_id", "api_key_id"),
	}
}

func (APIKeyGrantedGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("api_key_id"),
		field.Int64("group_id"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (APIKeyGrantedGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_key", APIKey.Type).
			Unique().
			Required().
			Field("api_key_id"),
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (APIKeyGrantedGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
	}
}
