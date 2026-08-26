import { gqlRequest } from "@/lib/graphql-client";

export type AuthPayload = {
  accessToken: string;
  userId: string;
};

const REGISTER_MUTATION = /* GraphQL */ `
  mutation Register($email: String!, $password: String!) {
    register(email: $email, password: $password) {
      accessToken
      userId
    }
  }
`;

const LOGIN_MUTATION = /* GraphQL */ `
  mutation Login($email: String!, $password: String!) {
    login(email: $email, password: $password) {
      accessToken
      userId
    }
  }
`;

export async function register(email: string, password: string): Promise<AuthPayload> {
  const data = await gqlRequest<{ register: AuthPayload }>(REGISTER_MUTATION, { email, password });
  return data.register;
}

export async function login(email: string, password: string): Promise<AuthPayload> {
  const data = await gqlRequest<{ login: AuthPayload }>(LOGIN_MUTATION, { email, password });
  return data.login;
}

export type UserSkill = {
  skill: { id: string; name: string; category: string };
  proficiency: string;
};

export type Profile = {
  userId: string;
  displayName: string;
  bio: string;
  skills: UserSkill[];
};

const MY_PROFILE_QUERY = /* GraphQL */ `
  query MyProfile {
    myProfile {
      userId
      displayName
      bio
      skills {
        proficiency
        skill {
          id
          name
          category
        }
      }
    }
  }
`;

export async function fetchMyProfile(token: string): Promise<Profile | null> {
  const data = await gqlRequest<{ myProfile: Profile | null }>(MY_PROFILE_QUERY, undefined, token);
  return data.myProfile;
}

const ADD_USER_SKILL_MUTATION = /* GraphQL */ `
  mutation AddUserSkill($skillId: ID!, $proficiency: String!) {
    addUserSkill(skillId: $skillId, proficiency: $proficiency)
  }
`;

export async function addUserSkill(
  token: string,
  skillId: string,
  proficiency: string,
): Promise<boolean> {
  const data = await gqlRequest<{ addUserSkill: boolean }>(
    ADD_USER_SKILL_MUTATION,
    { skillId, proficiency },
    token,
  );
  return data.addUserSkill;
}

const UPDATE_PROFILE_MUTATION = /* GraphQL */ `
  mutation UpdateProfile($displayName: String, $bio: String) {
    updateProfile(displayName: $displayName, bio: $bio) {
      userId
      displayName
      bio
    }
  }
`;

export async function updateProfile(
  token: string,
  displayName: string,
  bio: string,
): Promise<Profile> {
  const data = await gqlRequest<{ updateProfile: Profile }>(
    UPDATE_PROFILE_MUTATION,
    { displayName, bio },
    token,
  );
  return data.updateProfile;
}
