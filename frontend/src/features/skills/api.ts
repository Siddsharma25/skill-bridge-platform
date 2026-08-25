import { gqlRequest } from "@/lib/graphql-client";

export type Skill = {
  id: string;
  name: string;
  category: string;
};

const SKILLS_QUERY = /* GraphQL */ `
  query Skills {
    skills {
      id
      name
      category
    }
  }
`;

const CREATE_SKILL_MUTATION = /* GraphQL */ `
  mutation CreateSkill($name: String!, $category: String!) {
    createSkill(name: $name, category: $category) {
      id
      name
      category
    }
  }
`;

export async function fetchSkills(): Promise<Skill[]> {
  const data = await gqlRequest<{ skills: Skill[] }>(SKILLS_QUERY);
  return data.skills;
}

export async function createSkill(name: string, category: string): Promise<Skill> {
  const data = await gqlRequest<{ createSkill: Skill }>(CREATE_SKILL_MUTATION, { name, category });
  return data.createSkill;
}
