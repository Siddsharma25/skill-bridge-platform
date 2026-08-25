import { gqlRequest } from "@/lib/graphql-client";
import type { Skill } from "@/features/skills/api";

export type JobMatch = {
  userId: string;
  score: number;
  matchedAt: string;
};

export type Job = {
  id: string;
  title: string;
  description: string;
  requiredSkills: Skill[];
  matches: JobMatch[];
};

const JOB_FIELDS = /* GraphQL */ `
  id
  title
  description
  requiredSkills {
    id
    name
    category
  }
  matches {
    userId
    score
    matchedAt
  }
`;

const JOBS_QUERY = /* GraphQL */ `
  query Jobs {
    jobs {
      ${JOB_FIELDS}
    }
  }
`;

const JOB_QUERY = /* GraphQL */ `
  query Job($id: ID!) {
    job(id: $id) {
      ${JOB_FIELDS}
    }
  }
`;

const CREATE_JOB_MUTATION = /* GraphQL */ `
  mutation CreateJob($title: String!, $description: String!, $requiredSkillIds: [ID!]!) {
    createJob(title: $title, description: $description, requiredSkillIds: $requiredSkillIds) {
      ${JOB_FIELDS}
    }
  }
`;

export async function fetchJobs(): Promise<Job[]> {
  const data = await gqlRequest<{ jobs: Job[] }>(JOBS_QUERY);
  return data.jobs;
}

export async function fetchJob(id: string): Promise<Job | null> {
  const data = await gqlRequest<{ job: Job | null }>(JOB_QUERY, { id });
  return data.job;
}

export async function createJob(
  title: string,
  description: string,
  requiredSkillIds: string[],
): Promise<Job> {
  const data = await gqlRequest<{ createJob: Job }>(CREATE_JOB_MUTATION, {
    title,
    description,
    requiredSkillIds,
  });
  return data.createJob;
}
