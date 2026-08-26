import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { fetchMyProfile, addUserSkill, updateProfile } from "@/features/auth/api";
import { fetchSkills } from "@/features/skills/api";
import { useAuthStore } from "@/lib/auth-store";
import { GraphQLError } from "@/lib/graphql-client";
import { chipColor, cn, gradientButton } from "@/lib/utils";

const profileSchema = z.object({
  displayName: z.string(),
  bio: z.string(),
});

type ProfileValues = z.infer<typeof profileSchema>;

const addSkillSchema = z.object({
  skillId: z.string().min(1, "Pick a skill"),
  proficiency: z.string().min(1, "Required"),
});

type AddSkillValues = z.infer<typeof addSkillSchema>;

export function ProfilePage() {
  const queryClient = useQueryClient();
  const { accessToken, userId } = useAuthStore();

  const { data: profile, isLoading, isError } = useQuery({
    queryKey: ["myProfile", userId],
    queryFn: () => fetchMyProfile(accessToken!),
    enabled: !!accessToken,
  });

  const { data: skills } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });

  const profileForm = useForm<ProfileValues>({
    resolver: zodResolver(profileSchema),
    defaultValues: { displayName: "", bio: "" },
  });

  // Seeds the edit form once the profile loads (or changes) — defaultValues
  // only apply on the form's first mount, and the query resolves after that.
  useEffect(() => {
    if (profile) {
      profileForm.reset({ displayName: profile.displayName, bio: profile.bio });
    }
  }, [profile, profileForm]);

  const updateProfileMutation = useMutation({
    mutationFn: ({ displayName, bio }: ProfileValues) =>
      updateProfile(accessToken!, displayName, bio),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myProfile", userId] });
    },
  });

  const addSkillForm = useForm<AddSkillValues>({
    resolver: zodResolver(addSkillSchema),
    defaultValues: { skillId: "", proficiency: "" },
  });

  const addSkillMutation = useMutation({
    mutationFn: ({ skillId, proficiency }: AddSkillValues) =>
      addUserSkill(accessToken!, skillId, proficiency),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myProfile", userId] });
      addSkillForm.reset();
    },
  });

  return (
    <div className="mx-auto max-w-md p-8">
      <Card className="animate-in-up">
        <CardHeader>
          <CardTitle className="bg-gradient-to-r from-fuchsia-600 via-violet-600 to-sky-600 bg-clip-text text-transparent">
            My profile
          </CardTitle>
          <CardDescription>userId: {userId}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-6">
          {isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
          {isError && (
            <p className="text-destructive text-sm">
              Couldn't load your profile. Try refreshing — if it keeps happening, log out and back
              in from the nav.
            </p>
          )}

          {profile && (
            <>
              <Form {...profileForm}>
                <form
                  className="grid gap-4"
                  onSubmit={profileForm.handleSubmit((values) =>
                    updateProfileMutation.mutate(values),
                  )}
                >
                  <FormField
                    control={profileForm.control}
                    name="displayName"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Display name</FormLabel>
                        <FormControl>
                          <Input placeholder="Not set yet" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={profileForm.control}
                    name="bio"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Bio</FormLabel>
                        <FormControl>
                          <Textarea rows={3} placeholder="Not set yet" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  {updateProfileMutation.isError && (
                    <p className="text-destructive text-sm">
                      {updateProfileMutation.error instanceof GraphQLError
                        ? updateProfileMutation.error.message
                        : "Something went wrong. Try again."}
                    </p>
                  )}
                  <Button
                    type="submit"
                    size="sm"
                    className={cn(gradientButton, "justify-self-start")}
                    disabled={updateProfileMutation.isPending || !profileForm.formState.isDirty}
                  >
                    {updateProfileMutation.isPending ? "Saving..." : "Save"}
                  </Button>
                </form>
              </Form>

              <div className="grid gap-3 border-t pt-4">
                <p className="text-sm font-medium">My skills</p>
                {profile.skills.length === 0 ? (
                  <p className="text-muted-foreground text-sm">No skills added yet.</p>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {profile.skills.map((s) => (
                      <span
                        key={s.skill.id}
                        className={cn(
                          "flex items-center gap-1.5 rounded-full px-3 py-1 text-sm font-medium",
                          chipColor(s.skill.id),
                        )}
                      >
                        {s.skill.name}
                        <span className="text-xs opacity-70">{s.proficiency}</span>
                      </span>
                    ))}
                  </div>
                )}

                <Form {...addSkillForm}>
                  <form
                    className="grid gap-3"
                    onSubmit={addSkillForm.handleSubmit((values) =>
                      addSkillMutation.mutate(values),
                    )}
                  >
                    <div className="flex gap-2">
                      <FormField
                        control={addSkillForm.control}
                        name="skillId"
                        render={({ field }) => (
                          <FormItem className="flex-1">
                            <FormControl>
                              <select
                                className="border-input focus-visible:ring-ring/50 h-9 w-full cursor-pointer rounded-md border bg-transparent px-2 text-sm outline-none focus-visible:ring-2"
                                {...field}
                              >
                                <option value="">
                                  {skills?.length ? "Select a skill..." : "No skills exist yet"}
                                </option>
                                {skills?.map((s) => (
                                  <option key={s.id} value={s.id}>
                                    {s.name}
                                  </option>
                                ))}
                              </select>
                            </FormControl>
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={addSkillForm.control}
                        name="proficiency"
                        render={({ field }) => (
                          <FormItem className="flex-1">
                            <FormControl>
                              <Input placeholder="Proficiency" {...field} />
                            </FormControl>
                          </FormItem>
                        )}
                      />
                    </div>
                    {!skills?.length && (
                      <p className="text-muted-foreground text-xs">
                        Add a skill on the Skills page first.
                      </p>
                    )}
                    {addSkillMutation.isError && (
                      <p className="text-destructive text-sm">
                        {addSkillMutation.error instanceof GraphQLError
                          ? addSkillMutation.error.message
                          : "Something went wrong. Try again."}
                      </p>
                    )}
                    <Button
                      type="submit"
                      variant="outline"
                      size="sm"
                      className="justify-self-start"
                      disabled={addSkillMutation.isPending || !skills?.length}
                    >
                      {addSkillMutation.isPending ? "Adding..." : "Add to my skills"}
                    </Button>
                  </form>
                </Form>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
